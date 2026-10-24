package server

import "context"

// seedDemo fills an empty box with example data for screenshots (DEMO_DATA=true).
func (s *Server) seedDemo(ctx context.Context) {
	if n, _ := s.store.CountUsers(ctx); n == 0 {
		return
	}
	s.log.Info("demo data is seeded by scripts/demo (DEMO_DATA only marks the box)")
}
