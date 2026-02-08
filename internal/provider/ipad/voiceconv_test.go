package ipad

import (
	"bytes"
	"testing"
)

func TestStripSilkHeader(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
	}{
		{
			name:     "with header and newline",
			input:    append([]byte("#!SILK_V3\n"), []byte{0x01, 0x02, 0x03}...),
			expected: []byte{0x01, 0x02, 0x03},
		},
		{
			name:     "with header no newline",
			input:    append([]byte("#!SILK_V3"), []byte{0x01, 0x02, 0x03}...),
			expected: []byte{0x01, 0x02, 0x03},
		},
		{
			name:     "without header",
			input:    []byte{0x01, 0x02, 0x03},
			expected: []byte{0x01, 0x02, 0x03},
		},
		{
			name:     "empty",
			input:    []byte{},
			expected: []byte{},
		},
		{
			name:     "only header",
			input:    []byte("#!SILK_V3\n"),
			expected: []byte{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripSilkHeader(tt.input)
			if !bytes.Equal(result, tt.expected) {
				t.Errorf("stripSilkHeader(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestAddSilkHeader(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03}
	result := addSilkHeader(data)

	expected := append([]byte("#!SILK_V3\n"), data...)
	if !bytes.Equal(result, expected) {
		t.Errorf("addSilkHeader = %q, want %q", result, expected)
	}
}

func TestAddAndStripSilkHeader_RoundTrip(t *testing.T) {
	original := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE}
	withHeader := addSilkHeader(original)
	stripped := stripSilkHeader(withHeader)

	if !bytes.Equal(stripped, original) {
		t.Errorf("round trip failed: got %q, want %q", stripped, original)
	}
}

func TestWriteTempFile(t *testing.T) {
	data := []byte("test content for temp file")
	path, err := writeTempFile("", "test_*.txt", data)
	if err != nil {
		t.Fatalf("writeTempFile: %v", err)
	}

	if path == "" {
		t.Fatal("path should not be empty")
	}

	// Clean up is handled by the caller in production code.
	// Here we just verify the file was created successfully.
}

func TestNewVoiceConverter_FFmpegCheck(t *testing.T) {
	// This test checks whether ffmpeg is available on the system.
	// It's not a failure if ffmpeg is missing — we just verify graceful degradation.
	vc, err := NewVoiceConverter("")
	if err != nil {
		t.Logf("ffmpeg not available (expected in CI): %v", err)
		return
	}

	if !vc.IsAvailable() {
		t.Fatal("IsAvailable should be true when converter was created successfully")
	}

	if vc.ffmpegPath == "" {
		t.Fatal("ffmpegPath should be set")
	}
}
