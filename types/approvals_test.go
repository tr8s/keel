package types

import "testing"

func TestApprovalDelta(t *testing.T) {
	const (
		currentDigest = "sha256:5f55a51b0c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7"
		newDigest     = "sha256:62c200e9a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5"
	)

	tests := []struct {
		name     string
		approval Approval
		want     string
	}{
		{
			name:     "different versions ignore digests",
			approval: Approval{CurrentVersion: "1.1.1", NewVersion: "1.1.2", CurrentDigest: currentDigest, NewDigest: newDigest},
			want:     "1.1.1 -> 1.1.2",
		},
		{
			name:     "same tag with digests",
			approval: Approval{CurrentVersion: "main", NewVersion: "main", CurrentDigest: currentDigest, NewDigest: newDigest},
			want:     "main@sha256:5f55a51b -> main@sha256:62c200e9",
		},
		{
			name:     "same tag with only the new digest",
			approval: Approval{CurrentVersion: "main", NewVersion: "main", NewDigest: newDigest},
			want:     "main -> main@sha256:62c200e9",
		},
		{
			name:     "same tag without digests",
			approval: Approval{CurrentVersion: "main", NewVersion: "main"},
			want:     "main -> main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.approval.Delta(); got != tt.want {
				t.Errorf("Delta() = %q, want %q", got, tt.want)
			}
		})
	}
}
