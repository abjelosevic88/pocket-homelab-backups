package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Backrest talks to a Backrest server's API (connect-RPC over JSON).
type Backrest struct {
	URL      string
	User     string
	Password string
	HTTP     *http.Client
}

// Configured reports whether a Backrest address is set.
func (b *Backrest) Configured() bool { return b != nil && b.URL != "" }

func (b *Backrest) call(ctx context.Context, method string, in, out any, timeout time.Duration) error {
	if !b.Configured() {
		return fmt.Errorf("no Backrest is configured (BACKREST_URL)")
	}
	body, _ := json.Marshal(in)
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.URL+"/v1.Backrest/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.User != "" {
		req.SetBasicAuth(b.User, b.Password)
	}
	hc := b.HTTP
	if hc == nil {
		hc = &http.Client{}
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		msg := strings.TrimSpace(firstNonEmpty(e.Message, string(data)))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("backrest %s: HTTP %d %s", method, res.StatusCode, msg)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// BRConfig is the part of Backrest's config this app reads. Raw keeps the
// whole document so a change writes back everything else untouched.
type BRConfig struct {
	Modno    int64          `json:"modno"`
	Instance string         `json:"instance"`
	Repos    []BRRepo       `json:"repos"`
	Plans    []BRPlan       `json:"plans"`
	Raw      map[string]any `json:"-"`
}

// BRRepo is a Backrest repository.
type BRRepo struct {
	ID       string   `json:"id"`
	URI      string   `json:"uri"`
	Password string   `json:"password"`
	Env      []string `json:"env"`
}

// BRPlan is a Backrest plan.
type BRPlan struct {
	ID        string          `json:"id"`
	Repo      string          `json:"repo"`
	Paths     []string        `json:"paths"`
	Excludes  []string        `json:"excludes"`
	Schedule  json.RawMessage `json:"schedule"`
	Retention json.RawMessage `json:"retention"`
	Hooks     []BRHook        `json:"hooks"`
}

// BRHook is a hook as Backrest stores it.
type BRHook struct {
	Conditions    []string `json:"conditions"`
	OnError       string   `json:"onError,omitempty"`
	ActionCommand *struct {
		Command string `json:"command"`
	} `json:"actionCommand,omitempty"`
}

// Cron returns a plan's cron line ("" = manual / disabled).
func (p BRPlan) Cron() string {
	var s struct {
		Cron     string `json:"cron"`
		Disabled bool   `json:"disabled"`
	}
	_ = json.Unmarshal(p.Schedule, &s)
	if s.Disabled {
		return ""
	}
	return s.Cron
}

// GetConfig reads Backrest's configuration.
func (b *Backrest) GetConfig(ctx context.Context) (*BRConfig, error) {
	var raw map[string]any
	if err := b.call(ctx, "GetConfig", map[string]any{}, &raw, 20*time.Second); err != nil {
		return nil, err
	}
	data, _ := json.Marshal(raw)
	var c BRConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	c.Raw = raw
	return &c, nil
}

// SetConfig writes Backrest's configuration back (raw document).
func (b *Backrest) SetConfig(ctx context.Context, raw map[string]any) error {
	return b.call(ctx, "SetConfig", raw, nil, 30*time.Second)
}

// BROperation is one entry of Backrest's operation log.
type BROperation struct {
	ID         string `json:"id"`
	FlowID     string `json:"flowId"`
	RepoID     string `json:"repoId"`
	PlanID     string `json:"planId"`
	SnapshotID string `json:"snapshotId"`
	Status     string `json:"status"`
	Start      string `json:"unixTimeStartMs"`
	End        string `json:"unixTimeEndMs"`
	Message    string `json:"displayMessage"`
	Backup     *struct {
		LastStatus *struct {
			Summary *struct {
				SnapshotID   string `json:"snapshotId"`
				DataAdded    string `json:"dataAdded"`
				TotalBytes   string `json:"totalBytesProcessed"`
				FilesNew     string `json:"filesNew"`
				FilesChanged string `json:"filesChanged"`
			} `json:"summary"`
		} `json:"lastStatus"`
		Errors []struct {
			Item    string `json:"item"`
			Message string `json:"message"`
		} `json:"errors"`
	} `json:"operationBackup,omitempty"`
	Forget  json.RawMessage `json:"operationForget,omitempty"`
	Prune   json.RawMessage `json:"operationPrune,omitempty"`
	Check   json.RawMessage `json:"operationCheck,omitempty"`
	RunHook *struct {
		Name      string `json:"name"`
		Condition string `json:"condition"`
	} `json:"operationRunHook,omitempty"`
	IndexSnapshot json.RawMessage `json:"operationIndexSnapshot,omitempty"`
	Restore       json.RawMessage `json:"operationRestore,omitempty"`
}

// Kind names what the operation was.
func (o BROperation) Kind() string {
	switch {
	case o.Backup != nil:
		return "backup"
	case len(o.Forget) > 0:
		return "forget"
	case len(o.Prune) > 0:
		return "prune"
	case len(o.Check) > 0:
		return "check"
	case o.RunHook != nil:
		return "hook"
	case len(o.IndexSnapshot) > 0:
		return "index"
	case len(o.Restore) > 0:
		return "restore"
	}
	return "other"
}

// StartTime returns when the operation started.
func (o BROperation) StartTime() time.Time { return msTime(o.Start) }

// EndTime returns when the operation ended (zero while it runs).
func (o BROperation) EndTime() time.Time { return msTime(o.End) }

func msTime(s string) time.Time {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(n)
}

// Operations returns the newest operations (of a plan or repo when given).
func (b *Backrest) Operations(ctx context.Context, plan, repo string, lastN int) ([]BROperation, error) {
	sel := map[string]any{}
	if plan != "" {
		sel["planId"] = plan
	}
	if repo != "" {
		sel["repoId"] = repo
	}
	var out struct {
		Operations []BROperation `json:"operations"`
	}
	err := b.call(ctx, "GetOperations", map[string]any{"selector": sel, "lastN": strconv.Itoa(lastN)}, &out, 30*time.Second)
	return out.Operations, err
}

// Backup starts a plan now and waits until Backrest says it is done.
func (b *Backrest) Backup(ctx context.Context, plan string) error {
	return b.call(ctx, "Backup", map[string]any{"value": plan}, nil, 0)
}
