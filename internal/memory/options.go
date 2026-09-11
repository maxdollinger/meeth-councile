package memory

import "log/slog"

// Option configures a Memory.
type Option func(*Memory)

// WithLogger sets the logger used to record writes. A nil logger is ignored.
func WithLogger(logger *slog.Logger) Option {
	return func(m *Memory) {
		if logger != nil {
			m.logger = logger
		}
	}
}
