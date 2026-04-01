package parser_test

import (
	"slices"
	"testing"
	"vortex/internal/parser"
)

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantURL string
		wantOK  bool
	}{
		{
			name:    "valid http URL",
			input:   "http://example.com/path",
			wantURL: "http://example.com/path",
			wantOK:  true,
		},
		{
			name:    "valid https URL",
			input:   "https://example.com/path",
			wantURL: "https://example.com/path",
			wantOK:  true,
		},
		{
			name:    "strips fragment",
			input:   "https://example.com/path#fragment",
			wantURL: "https://example.com/path",
			wantOK:  true,
		},
		{
			name:    "reject ftp",
			input:   "ftp://example.com/file",
			wantURL: "",
			wantOK:  false,
		},
		{
			name:    "reject mailto",
			input:   "mailto:user@example.com",
			wantURL: "",
			wantOK:  false,
		},
		{
			name:    "reject javascript",
			input:   "javascript:alert('XSS')",
			wantURL: "",
			wantOK:  false,
		},
		{
			name:    "reject data URL",
			input:   "data:text/plain;base64,SGVsbG8sIFdvcmxkIQ==",
			wantURL: "",
			wantOK:  false,
		},
		{
			name:    "reject empty string",
			input:   "",
			wantURL: "",
			wantOK:  false,
		},
		{
			name:    "invalid URL format",
			input:   "://",
			wantURL: "",
			wantOK:  false,
		},
		{
			name:    "reject protocol relative URL",
			input:   "//example.com/path",
			wantURL: "",
			wantOK:  false,
		},
		{
			name:    "URL with query parameters",
			input:   "https://example.com/path?query=1&u=https://example.com/embedded",
			wantURL: "https://example.com/path?query=1&u=https://example.com/embedded",
			wantOK:  true,
		},
		{
			name:    "URL with port",
			input:   "https://example.com:8080/path",
			wantURL: "https://example.com:8080/path",
			wantOK:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotOK := parser.SanitizeURL(tt.input)
			if gotURL != tt.wantURL || gotOK != tt.wantOK {
				t.Errorf("SanitizeURL(%q) = (%q, %v), want (%q, %v)",
					tt.input, gotURL, gotOK, tt.wantURL, tt.wantOK)
			}
		})
	}
}

func TestExtractURLs(t *testing.T) {
	tests := []struct {
		name    string
		html    string
		baseURL string
		want    []string
		wantErr bool
	}{
		{
			name:    "Invalid base URL",
			html:    `<html><body></body></html>`,
			baseURL: "://",
			want:    nil,
			wantErr: true,
		},
		// HTML Parsing goquery error is nigh impossible to trigger - no test;
		{
			name:    "No URLs",
			html:    `<html><body><p>No links here</p></body></html>`,
			baseURL: "https://example.com",
			want:    nil,
			wantErr: false,
		},
		{
			name: "Valid hrefs",
			html: `<html><body>
					<a href="https://example.com/absolute">Absolute</a>
					<a href="/relative">Relative</a>
					<a href="//example.com/page">Protocol-relative</a>
					<a href="https://example.com?q=1&u=https://example.com/embedded">With query</a>
					<a href="https://example.com/with#fragment">With fragment</a>
					</body></html>`,
			baseURL: "https://example.com",
			want: []string{"https://example.com/absolute",
				"https://example.com/relative",
				"https://example.com/page",
				"https://example.com?q=1&u=https://example.com/embedded",
				"https://example.com/with#fragment"},
			wantErr: false,
		},
		{
			// Note: goquery will include the invalid URLs as-is, but url.Parse() will fail & skip them silently
			name: "Invalid hrefs",
			html: `<html><body>
					<a href="https://example.com/valid">Valid</a>
					<a href="https://example.com/also-valid">Also Valid</a>
					<a href="://">Invalid</a>
					</body></html>`,
			baseURL: "https://example.com",
			want: []string{"https://example.com/valid",
				"https://example.com/also-valid"},
			wantErr: false,
		},
		{
			name:    "Empty href",
			html:    `<html><body><a href="">Empty</a></body></html>`,
			baseURL: "https://example.com",
			want:    []string{"https://example.com"},
			wantErr: false,
		},
		{
			name: "Multiple href with duplicates",
			html: `<html><body>
					<a href="https://example.com/page1">Page 1</a>
					<a href="https://example.com/page1">Page 1 Duplicate</a>
					<a href="https://example.com/page2">Page 2</a>
					</body></html>`,
			baseURL: "https://example.com",
			want:    []string{"https://example.com/page1", "https://example.com/page1", "https://example.com/page2"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.ExtractURLs([]byte(tt.html), tt.baseURL)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractURLs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("ExtractURLs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractText(t *testing.T) {
	tests := []struct {
		name    string
		html    []byte
		want    string
		wantErr bool
	}{
		{
			name:    "Extract text from HTML",
			html:    []byte("<html><body><p>Hello, World!</p></body></html>"),
			want:    "Hello, World!",
			wantErr: false,
		},
		{
			name:    "Extract text with nested tags",
			html:    []byte("<html><body><div><p>Nested <b>text</b></p></div></body></html>"),
			want:    "Nested text",
			wantErr: false,
		},
		{
			name:    "Extract text with excluded tags",
			html:    []byte("<html><body><script>var x = 1;</script><p>Visible text</p></body></html>"),
			want:    "Visible text",
			wantErr: false,
		},
		{
			name:    "Whitespace collapse",
			html:    []byte("<html><body><p>   Multiple    spaces   </p></body></html>"),
			want:    "Multiple spaces",
			wantErr: false,
		},
		{
			name:    "Empty HTML",
			html:    []byte(""),
			want:    "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.ExtractText(tt.html)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractText() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ExtractText() = %v, want %v", got, tt.want)
			}
		})
	}
}
