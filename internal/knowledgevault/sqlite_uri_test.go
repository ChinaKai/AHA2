package knowledgevault

import "testing"

func TestSQLiteFileDSN(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		query string
		want  string
	}{
		{
			name: "posix path",
			path: "/tmp/aha/knowledge-vault-index.db",
			want: "file:///tmp/aha/knowledge-vault-index.db",
		},
		{
			name:  "posix path keeps query",
			path:  "/tmp/aha/knowledge-vault-index.db",
			query: "mode=ro&_pragma=busy_timeout(5000)",
			want:  "file:///tmp/aha/knowledge-vault-index.db?mode=ro&_pragma=busy_timeout(5000)",
		},
		{
			name: "windows drive with backslashes",
			path: `D:\ProgramData\AHA2\knowledge-vault-index.db`,
			want: "file:///D:/ProgramData/AHA2/knowledge-vault-index.db",
		},
		{
			name: "windows drive with forward slashes",
			path: "D:/ProgramData/AHA2/knowledge-vault-index.db",
			want: "file:///D:/ProgramData/AHA2/knowledge-vault-index.db",
		},
		{
			name:  "windows drive keeps query",
			path:  `D:\ProgramData\AHA2\knowledge-vault-index.db`,
			query: "mode=ro&_pragma=busy_timeout(5000)",
			want:  "file:///D:/ProgramData/AHA2/knowledge-vault-index.db?mode=ro&_pragma=busy_timeout(5000)",
		},
		{
			name: "lowercase windows drive",
			path: `c:\temp\aha.db`,
			want: "file:///c:/temp/aha.db",
		},
		{
			name:  "query with leading question mark is not doubled",
			path:  `D:\temp\aha.db`,
			query: "?mode=ro",
			want:  "file:///D:/temp/aha.db?mode=ro",
		},
		{
			name: "windows path with space is escaped",
			path: `D:\Program Files\aha.db`,
			want: "file:///D:/Program%20Files/aha.db",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SQLiteFileDSN(test.path, test.query); got != test.want {
				t.Fatalf("SQLiteFileDSN(%q, %q) = %q, want %q", test.path, test.query, got, test.want)
			}
		})
	}
}
