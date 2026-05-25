package domain

import "testing"

func TestPendingStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		name   string
		status PendingStatus
		want   bool
	}{
		{
			name:   "pending_approval is not terminal",
			status: PendingStatusPendingApproval,
			want:   false,
		},
		{
			name:   "approved_applied is terminal",
			status: PendingStatusApprovedApplied,
			want:   true,
		},
		{
			name:   "denied is terminal",
			status: PendingStatusDenied,
			want:   true,
		},
		{
			name:   "timeout is terminal",
			status: PendingStatusTimeout,
			want:   true,
		},
		{
			name:   "unknown empty status is not terminal (= conservative)",
			status: PendingStatus(""),
			want:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.status.IsTerminal()
			if got != tc.want {
				t.Errorf("PendingStatus(%q).IsTerminal() = %v, want %v",
					tc.status, got, tc.want)
			}
		})
	}
}

func TestPendingOp_StringValues(t *testing.T) {
	// ADR 0039 §gate flow で pin した op identifier。 Firestore document 内の
	// op field と一致する必要があるため、 文字列値が変わると drift が起きる。
	if string(PendingOpAdd) != "add" {
		t.Errorf("PendingOpAdd = %q, want %q", PendingOpAdd, "add")
	}
	if string(PendingOpArchive) != "archive" {
		t.Errorf("PendingOpArchive = %q, want %q", PendingOpArchive, "archive")
	}
}

func TestPendingStatus_StringValues(t *testing.T) {
	// ADR 0039 §lifecycle で pin した status identifier。 Firestore document 内
	// の status field と client polling response の `status` JSON field が
	// この文字列値と一致する必要がある。
	cases := map[PendingStatus]string{
		PendingStatusPendingApproval: "pending_approval",
		PendingStatusApprovedApplied: "approved_applied",
		PendingStatusDenied:          "denied",
		PendingStatusTimeout:         "timeout",
	}
	for status, want := range cases {
		if string(status) != want {
			t.Errorf("PendingStatus(%q) string value = %q, want %q",
				status, string(status), want)
		}
	}
}
