package queuesvc

import "context"

// WithAdmission fences new queued work while the owning backend drains.
func (s *Service) WithAdmission(begin func(context.Context, string) (func(), error)) *Service {
	s.beginAdmission = begin
	return s
}
