package flavor

import (
	"strings"
	"testing"
)

func TestInterfaceNumber(t *testing.T) {
	t.Parallel()
	tests := []struct {
		version string
		want    int // 0 means an error
	}{
		{"1.60.1.70938", 16001},
		{"12.1.5.66102", 120105},
		{"5.5.4.61000", 50504},
		{"1.15.9.60000", 11509},
		{"1.60.1", 16001},
		{"1.60", 0},
		{"1.100.0.1", 0},
		{"1.2.x.1", 0},
		{"0.1.0.1", 0},
		{"-1.1.0.1", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got, err := interfaceNumber(tt.version)
		if tt.want == 0 {
			if err == nil {
				t.Errorf("interfaceNumber(%q) = %d, want error", tt.version, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("interfaceNumber(%q) = %d, %v; want %d", tt.version, got, err, tt.want)
		}
	}
}

func TestProductVersion(t *testing.T) {
	t.Parallel()
	const header = "Branch!STRING:0|Active!DEC:1|Version!STRING:0|Product!STRING:0\r\n"
	tests := []struct {
		name    string
		file    string
		want    string
		wantErr bool
	}{
		{
			name: "crlf",
			file: header + "us|1|1.60.1.70938|wowf\r\n",
			want: "1.60.1.70938",
		},
		{
			name: "active row wins",
			file: header + "us|0|1.59.0.1|wowf\neu|1|1.60.1.2|wowf\nkr|0|1.58.0.3|wowf\n",
			want: "1.60.1.2",
		},
		{
			name: "first row when none is active",
			file: header + "us|0|1.59.0.1|wowf\neu|0|1.60.1.2|wowf\n",
			want: "1.59.0.1",
		},
		{
			name: "short rows are skipped",
			file: header + "us|1\nus|1|1.60.1.2|wowf\n",
			want: "1.60.1.2",
		},
		{name: "other product only", file: header + "us|1|12.1.5.1|wow\n", wantErr: true},
		{name: "no version column", file: "Branch!STRING:0|Product!STRING:0\nus|wowf\n", wantErr: true},
		{name: "empty", file: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := productVersion(strings.NewReader(tt.file), "wowf")
			if tt.wantErr {
				if err == nil {
					t.Errorf("productVersion = %q, want error", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("productVersion = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
