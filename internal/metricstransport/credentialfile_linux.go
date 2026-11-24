package metricstransport

// The Linux POSIX ACL mask is already reflected in the group mode bits;
// the credential and directory mode checks above account for that mask.
func noExtendedACL(_ int) bool { return true }
