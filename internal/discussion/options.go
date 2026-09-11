package discussion

import (
	"log/slog"
	"math/rand"
)

// Option configures a Discussion.
type Option func(*Discussion)

// WithLogger sets the logger used to record the discussion's progress. A nil
// logger is ignored.
func WithLogger(logger *slog.Logger) Option {
	return func(d *Discussion) {
		if logger != nil {
			d.logger = logger
		}
	}
}

// WithMaxRounds caps how many rounds Run may hold. Values below 1 are ignored.
func WithMaxRounds(n int) Option {
	return func(d *Discussion) {
		if n >= 1 {
			d.maxRounds = n
		}
	}
}

// WithRand sets the random source used for turn order and model assignment.
// A nil source is ignored.
func WithRand(r *rand.Rand) Option {
	return func(d *Discussion) {
		if r != nil {
			d.rng = r
		}
	}
}
