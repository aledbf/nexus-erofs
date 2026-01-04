// Package stringutil provides string manipulation utilities.
package stringutil

// TruncateOutput truncates command output to maxLen bytes for inclusion in error
// messages. This prevents verbose tool output from overwhelming error logs.
// If the output is shorter than maxLen, it is returned unchanged.
func TruncateOutput(out []byte, maxLen int) string {
	if len(out) <= maxLen {
		return string(out)
	}
	return string(out[:maxLen]) + "... (truncated)"
}
