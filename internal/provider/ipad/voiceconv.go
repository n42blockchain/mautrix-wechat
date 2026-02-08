package ipad

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// VoiceConverter handles conversion between WeChat silk format and standard audio formats.
// WeChat uses silk v3 encoding for voice messages, while Matrix expects ogg/opus.
type VoiceConverter struct {
	// ffmpegPath is the path to the ffmpeg binary.
	ffmpegPath string
	// silkDecoderPath is the path to the silk_v3_decoder binary.
	silkDecoderPath string
	// silkEncoderPath is the path to the silk_v3_encoder binary (optional, for Matrix→WeChat).
	silkEncoderPath string
	// tempDir for intermediate files.
	tempDir string
}

// NewVoiceConverter creates a new voice converter.
// It validates that required external tools are available.
func NewVoiceConverter(tempDir string) (*VoiceConverter, error) {
	vc := &VoiceConverter{
		tempDir: tempDir,
	}

	if tempDir == "" {
		vc.tempDir = os.TempDir()
	}

	// Locate ffmpeg
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found in PATH: %w", err)
	}
	vc.ffmpegPath = path

	// Locate silk decoder (optional — we can also use ffmpeg with silk plugin)
	if path, err := exec.LookPath("silk_v3_decoder"); err == nil {
		vc.silkDecoderPath = path
	}

	// Locate silk encoder (optional)
	if path, err := exec.LookPath("silk_v3_encoder"); err == nil {
		vc.silkEncoderPath = path
	}

	return vc, nil
}

// SilkToOgg converts a silk v3 audio stream to ogg/opus format.
// Returns the converted audio data and any error.
func (vc *VoiceConverter) SilkToOgg(silkData io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(silkData)
	if err != nil {
		return nil, fmt.Errorf("read silk data: %w", err)
	}

	// Strip the optional "#!SILK_V3" header added by WeChat
	raw = stripSilkHeader(raw)

	if vc.silkDecoderPath != "" {
		return vc.silkToOggViaDedicated(raw)
	}
	return vc.silkToOggViaFFmpeg(raw)
}

// OggToSilk converts an ogg/opus audio stream to silk v3 format for sending to WeChat.
func (vc *VoiceConverter) OggToSilk(oggData io.Reader) ([]byte, error) {
	if vc.silkEncoderPath == "" {
		return nil, fmt.Errorf("silk_v3_encoder not found — cannot convert to silk")
	}

	raw, err := io.ReadAll(oggData)
	if err != nil {
		return nil, fmt.Errorf("read ogg data: %w", err)
	}

	// Step 1: ogg/opus → PCM (s16le, 24000Hz, mono) via ffmpeg
	pcm, err := vc.ffmpegConvert(raw, []string{
		"-f", "ogg",
	}, []string{
		"-f", "s16le",
		"-ar", "24000",
		"-ac", "1",
		"-acodec", "pcm_s16le",
	})
	if err != nil {
		return nil, fmt.Errorf("ogg to pcm: %w", err)
	}

	// Step 2: PCM → silk via silk_v3_encoder
	pcmFile, err := writeTempFile(vc.tempDir, "pcm_*.raw", pcm)
	if err != nil {
		return nil, fmt.Errorf("write temp pcm: %w", err)
	}
	defer os.Remove(pcmFile)

	silkFile := strings.TrimSuffix(pcmFile, filepath.Ext(pcmFile)) + ".silk"
	defer os.Remove(silkFile)

	cmd := exec.Command(vc.silkEncoderPath, pcmFile, silkFile,
		"-Fs_API", "24000",
		"-rate", "24000",
		"-tencent",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("silk encode: %w, output: %s", err, string(output))
	}

	silkData, err := os.ReadFile(silkFile)
	if err != nil {
		return nil, fmt.Errorf("read silk output: %w", err)
	}

	// Prepend WeChat silk header
	return addSilkHeader(silkData), nil
}

// IsAvailable returns whether the converter has the minimum required tools.
func (vc *VoiceConverter) IsAvailable() bool {
	return vc.ffmpegPath != ""
}

// CanEncode returns whether the converter can encode to silk (for Matrix→WeChat).
func (vc *VoiceConverter) CanEncode() bool {
	return vc.silkEncoderPath != ""
}

// silkToOggViaDedicated uses the dedicated silk_v3_decoder binary.
// Flow: silk → PCM (via silk_v3_decoder) → ogg/opus (via ffmpeg)
func (vc *VoiceConverter) silkToOggViaDedicated(silkData []byte) ([]byte, error) {
	// Write silk data to temp file
	silkFile, err := writeTempFile(vc.tempDir, "silk_*.silk", silkData)
	if err != nil {
		return nil, fmt.Errorf("write temp silk: %w", err)
	}
	defer os.Remove(silkFile)

	// Decode silk → PCM
	pcmFile := strings.TrimSuffix(silkFile, filepath.Ext(silkFile)) + ".pcm"
	defer os.Remove(pcmFile)

	cmd := exec.Command(vc.silkDecoderPath, silkFile, pcmFile, "-Fs_API", "24000")
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("silk decode: %w, output: %s", err, string(output))
	}

	// Read PCM data
	pcmData, err := os.ReadFile(pcmFile)
	if err != nil {
		return nil, fmt.Errorf("read pcm: %w", err)
	}

	// Convert PCM → ogg/opus via ffmpeg
	return vc.ffmpegConvert(pcmData, []string{
		"-f", "s16le",
		"-ar", "24000",
		"-ac", "1",
	}, []string{
		"-c:a", "libopus",
		"-b:a", "64k",
		"-f", "ogg",
	})
}

// silkToOggViaFFmpeg attempts to convert silk to ogg purely via ffmpeg.
// This requires ffmpeg to be compiled with silk support or uses a fallback
// by trying to decode with the raw silk format hint.
func (vc *VoiceConverter) silkToOggViaFFmpeg(silkData []byte) ([]byte, error) {
	// Try direct ffmpeg conversion (works if ffmpeg has silk demuxer)
	result, err := vc.ffmpegConvert(silkData, []string{
		"-f", "silk",
		"-ar", "24000",
	}, []string{
		"-c:a", "libopus",
		"-b:a", "64k",
		"-f", "ogg",
	})
	if err == nil {
		return result, nil
	}

	// Fallback: try with generic audio input detection
	result, err = vc.ffmpegConvert(silkData, nil, []string{
		"-c:a", "libopus",
		"-b:a", "64k",
		"-f", "ogg",
	})
	if err != nil {
		return nil, fmt.Errorf("silk to ogg via ffmpeg failed: %w (silk_v3_decoder not available)", err)
	}
	return result, nil
}

// ffmpegConvert runs ffmpeg to convert audio data.
func (vc *VoiceConverter) ffmpegConvert(input []byte, inputArgs, outputArgs []string) ([]byte, error) {
	args := make([]string, 0, len(inputArgs)+len(outputArgs)+6)
	args = append(args, "-y", "-hide_banner", "-loglevel", "error")

	// Input arguments
	args = append(args, inputArgs...)
	args = append(args, "-i", "pipe:0")

	// Output arguments
	args = append(args, outputArgs...)
	args = append(args, "pipe:1")

	cmd := exec.Command(vc.ffmpegPath, args...)
	cmd.Stdin = bytes.NewReader(input)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w, stderr: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// stripSilkHeader removes the "#!SILK_V3\n" header that WeChat prepends.
func stripSilkHeader(data []byte) []byte {
	header := []byte("#!SILK_V3\n")
	if bytes.HasPrefix(data, header) {
		return data[len(header):]
	}
	// Also check without newline
	header2 := []byte("#!SILK_V3")
	if bytes.HasPrefix(data, header2) {
		return data[len(header2):]
	}
	return data
}

// addSilkHeader prepends the WeChat silk header.
func addSilkHeader(data []byte) []byte {
	header := []byte("#!SILK_V3\n")
	return append(header, data...)
}

// writeTempFile writes data to a temporary file and returns the path.
func writeTempFile(dir, pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path := f.Name()

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}
