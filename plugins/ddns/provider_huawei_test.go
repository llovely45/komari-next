package main

import "testing"

func TestHuaweiSignatureMatchesOfficialSDKVectors(t *testing.T) {
	const date = "20060102T150405Z"
	tests := []struct {
		name   string
		method string
		query  string
		body   []byte
		want   string
	}{
		{
			name:   "GET",
			method: "GET",
			query:  "limit=1",
			want:   "5a2ce64c865e0e6046321c6f3d5a77ba8413eeaf355c3166c03d58d02ac79624",
		},
		{
			name:   "POST JSON",
			method: "POST",
			query:  "key=value",
			body:   []byte("{\"Name\":\"test\",\"Id\":1}\n"),
			want:   "cecc2af119b18ab70b4d094c0750f3b42c02f254903179a0fc2cc72fc9db4f59",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := huaweiSignature(test.method, "/path", test.query, date, test.body, "SecretKey")
			if got != test.want {
				t.Fatalf("signature mismatch: got %s, want %s", got, test.want)
			}
		})
	}
}
