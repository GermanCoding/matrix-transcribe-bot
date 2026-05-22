package transcribe

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/GermanCoding/matrix-transcribe-bot/internal/config"
)

type Bridge struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	scan   *bufio.Scanner
	mu     sync.Mutex
	closed bool
}

type bridgeReq struct {
	AudioPath string `json:"audio_path"`
}

type bridgeResp struct {
	Text  string `json:"text"`
	Error string `json:"error"`
}

func NewBridge(cfg *config.Config) (*Bridge, error) {
	scriptPath := filepath.Join("src", "transcribe_bridge.py")
	cmd := exec.Command(cfg.PythonBin, scriptPath)
	cmd.Env = append(os.Environ(),
		"WHISPER_MODEL="+cfg.WhisperModel,
		"WHISPER_LANGUAGE="+cfg.WhisperLanguage,
		"WHISPER_MODEL_DIR="+cfg.WhisperModelDir,
		fmt.Sprintf("WHISPER_CPU_THREADS=%d", cfg.WhisperThreads),
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	go io.Copy(os.Stderr, stderr)

	return &Bridge{cmd: cmd, stdin: stdin, scan: bufio.NewScanner(stdout)}, nil
}

func (b *Bridge) Transcribe(audioPath string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return "", fmt.Errorf("bridge is closed")
	}

	enc := json.NewEncoder(b.stdin)
	if err := enc.Encode(bridgeReq{AudioPath: audioPath}); err != nil {
		return "", err
	}
	if !b.scan.Scan() {
		if err := b.scan.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("bridge exited unexpectedly")
	}

	var resp bridgeResp
	if err := json.Unmarshal(b.scan.Bytes(), &resp); err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", errors.New(resp.Error)
	}
	return resp.Text, nil
}

func (b *Bridge) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	_ = b.stdin.Close()
	return b.cmd.Wait()
}
