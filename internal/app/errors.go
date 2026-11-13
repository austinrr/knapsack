package app

// UserError is an error with a message and an optional hint meant for the
// user. Frontends find it with errors.As and render the hint on its own
// line. Err is the underlying cause, shown with --verbose.
type UserError struct {
	Msg  string
	Hint string
	Err  error
}

func (e *UserError) Error() string { return e.Msg }

// Unwrap lets errors.Is and errors.As see the cause.
func (e *UserError) Unwrap() error { return e.Err }
