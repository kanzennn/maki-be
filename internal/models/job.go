package models

// The outbox jobs that send the auth emails. The auth service enqueues them
// in the transaction of the change they are about; the worker's jobs send
// them.
//
//nolint:gosec // job kind names that mention passwords, not credentials
const (
	JobSendPasswordReset     = "auth.send_password_reset"
	JobSendEmailVerification = "auth.send_email_verification"
	JobSendPasswordChanged   = "auth.send_password_changed"
	JobSendEmailChangeNotice = "auth.send_email_change_notice"
)

// JobUser is the payload of a job about one user: the password reset and
// the password changed notice.
type JobUser struct {
	UserID string `json:"user_id"`
}

// JobEmail is the payload of the job that asks a user to confirm an
// address.
type JobEmail struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

// JobEmailChange is the payload of the notice to a user's old address that
// their email is changing.
type JobEmailChange struct {
	UserID   string `json:"user_id"`
	OldEmail string `json:"old_email"`
	NewEmail string `json:"new_email"`
}
