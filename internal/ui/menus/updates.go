package menus

import (
	"reflect"
	"slices"
	"time"

	"github.com/alexthvest/replipg/internal/core"
)

type Dependency struct {
	Kind      string
	Group     core.GroupID
	Character core.ID
	Order     core.ID
}

// Depend is called only by the runtime owner. Adding an index never extends TTL.
func (s *Sessions) Depend(session *Session, dependencies ...Dependency) {
	for _, dependency := range dependencies {
		if s.index[dependency] == nil {
			s.index[dependency] = map[string]bool{}
		}
		s.index[dependency][session.Token] = true
	}
}

func (s *Sessions) Invalidate(dependencies ...Dependency) {
	s.Sweep()
	for _, dependency := range dependencies {
		for token := range s.index[dependency] {
			if session := s.pool[token]; session != nil {
				session.Dirty = true
			}
		}
	}
}

func (s *Sessions) Remove(token string) {
	delete(s.pool, token)
	for dependency, tokens := range s.index {
		delete(tokens, token)
		if len(tokens) == 0 {
			delete(s.index, dependency)
		}
	}
}

func (s *Sessions) Dirty() []*Session {
	s.Sweep()
	var result []*Session
	for _, session := range s.pool {
		if session.Dirty && session.Message > 0 {
			result = append(result, session)
		}
	}
	return result
}

func detached(view core.View) core.View {
	view.Text = slices.Clone(view.Text)
	view.Rows = slices.Clone(view.Rows)
	for i := range view.Rows {
		view.Rows[i] = slices.Clone(view.Rows[i])
	}
	view.Menu = ""
	view.Feedback = ""
	return view
}

func (s *Sessions) Stage(session *Session, view core.View) {
	if s.pool[session.Token] != session {
		return
	}

	view = detached(view)
	if !reflect.DeepEqual(session.Desired, view) {
		session.Desired = view
		session.DesiredRevision++
	}
	if session.inFlight == 0 && reflect.DeepEqual(session.Delivered, view) {
		session.DeliveredRevision = session.DesiredRevision
	}
	session.Dirty = false
}

type Job struct {
	Token    string
	Group    core.GroupID
	Message  int
	Revision uint64
	View     core.View
}

func (s *Sessions) Claim(limit int) []Job {
	s.Sweep()
	var jobs []Job
	for _, session := range s.pool {
		if len(jobs) >= limit {
			break
		}
		if session.Message == 0 || session.Dirty || session.inFlight != 0 || session.DesiredRevision == session.DeliveredRevision || s.now().Before(session.retryAt) {
			continue
		}

		session.inFlight = session.DesiredRevision
		jobs = append(jobs, Job{
			Token:    session.Token,
			Group:    session.Group,
			Message:  session.Message,
			Revision: session.DesiredRevision,
			View:     detached(session.Desired),
		})
	}
	return jobs
}

func (s *Sessions) Active(job Job) bool {
	s.Sweep()
	session := s.pool[job.Token]
	return session != nil && session.Group == job.Group && session.Message == job.Message && session.inFlight == job.Revision
}

type Outcome struct {
	Job Job
	Err error
}

func (s *Sessions) Complete(outcome Outcome) {
	if !s.Active(outcome.Job) {
		return
	}

	session := s.pool[outcome.Job.Token]
	session.inFlight = 0
	result := Classify(outcome.Err)
	if result.Permanent {
		s.Remove(session.Token)
		return
	}

	if outcome.Err == nil || result.NotModified {
		session.Delivered = detached(outcome.Job.View)
		session.DeliveredRevision = outcome.Job.Revision
		session.failures = 0
		session.retryAt = time.Time{}
		return
	}

	session.failures = min(session.failures+1, 8)
	delay := min(time.Second*time.Duration(1<<session.failures), time.Minute)
	if result.RetryAfter > delay {
		delay = result.RetryAfter
	}
	session.retryAt = s.now().Add(delay)
}
