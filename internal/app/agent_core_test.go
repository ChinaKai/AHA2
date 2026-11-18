package app

import "testing"

func TestStrictSnapshotWritableFailsClosed(t *testing.T) {
	t.Parallel()
	scenarios := []struct {
		name       string
		permission string
		want       bool
	}{
		{name: "workspace write", permission: `{"filesystem":"workspace-write","approval":"never"}`, want: true},
		{name: "danger full access", permission: `{"approval":"auto","filesystem":"danger-full-access"}`, want: true},
		{name: "read only", permission: `{"filesystem":"read-only","approval":"never"}`, want: false},
		{name: "empty string", permission: "", want: false},
		{name: "empty object", permission: `{}`, want: false},
		{name: "invalid json", permission: `{"filesystem":`, want: false},
		{name: "null document", permission: `null`, want: false},
		{name: "null filesystem", permission: `{"filesystem":null,"approval":"never"}`, want: false},
		{name: "wrong filesystem type", permission: `{"filesystem":1,"approval":"never"}`, want: false},
		{name: "wrong approval type", permission: `{"filesystem":"workspace-write","approval":[]}`, want: false},
		{name: "missing filesystem", permission: `{"approval":"never"}`, want: false},
		{name: "missing approval", permission: `{"filesystem":"workspace-write"}`, want: false},
		{name: "unknown filesystem", permission: `{"filesystem":"readonly","approval":"never"}`, want: false},
		{name: "case variant", permission: `{"filesystem":"READ-ONLY","approval":"never"}`, want: false},
		{name: "unknown approval", permission: `{"filesystem":"workspace-write","approval":"always"}`, want: false},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			if got := strictSnapshotWritable(scenario.permission); got != scenario.want {
				t.Fatalf("strictSnapshotWritable(%q)=%v want %v", scenario.permission, got, scenario.want)
			}
		})
	}
}
