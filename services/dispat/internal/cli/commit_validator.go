// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/app"
	"github.com/yohimik/dispat/services/dispat/internal/config"
)

func runCommitMessageValidator(args []string, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "dispat: internal commit validator requires one message file")
		return 1
	}
	encoded, err := readCommitInput(os.Getenv("DISPAT_COMMIT_PARSER"), 1<<20)
	if err != nil {
		fmt.Fprintf(stderr, "dispat: read parser configuration: %v\n", err)
		return 1
	}
	var parserConfig ccme.Config
	if err := json.Unmarshal(encoded, &parserConfig); err != nil {
		fmt.Fprintf(stderr, "dispat: decode parser configuration: %v\n", err)
		return 1
	}

	message, err := cleanCommitMessage(args[0], os.Getenv("DISPAT_COMMIT_CLEANUP"))
	if err != nil {
		fmt.Fprintf(stderr, "dispat: clean commit message: %v\n", err)
		return 1
	}
	level := os.Getenv("DISPAT_COMMIT_LOG_LEVEL")
	if level != "trace" && level != "debug" {
		level = "warn"
	}
	log := newLogger(level, orDefault(os.Getenv("DISPAT_COMMIT_LOG_FORMAT"), "pretty"), stderr)
	validator := app.New(".", &config.File{ResolvedParser: parserConfig}, log)
	if err := validator.ValidateCommitMessage(message); err != nil {
		return 1
	}
	if err := replaceCommitMessage(args[0], message); err != nil {
		fmt.Fprintf(stderr, "dispat: write commit message: %v\n", err)
		return 1
	}
	return 0
}

// Private parser configuration and Git's temporary message must be ordinary
// files. Bound raw input separately from the parser's final-message limit:
// editor comments can legitimately make the raw file longer than the message.
func readCommitInput(path string, limit int) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("commit input must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("commit input exceeds %d bytes", limit)
	}
	return data, nil
}

func cleanCommitMessage(path, mode string) ([]byte, error) {
	message, err := readCommitInput(path, 16<<20)
	if err != nil {
		return nil, err
	}
	edited := false
	if marker := os.Getenv("DISPAT_COMMIT_EDITED"); marker != "" {
		if _, err := os.Stat(marker); err == nil {
			edited = true
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if mode == "default" {
		mode = "whitespace"
		if edited {
			mode = "strip"
		}
	}
	if mode == "scissors" && !edited {
		mode = "whitespace"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if mode == "verbatim" {
		return message, nil
	}
	if mode == "scissors" {
		comment := "#"
		if out, err := exec.CommandContext(ctx, "git", "config", "--get", "core.commentString").Output(); err == nil && strings.TrimSpace(string(out)) != "" {
			comment = strings.TrimSpace(string(out))
		} else if out, err := exec.CommandContext(ctx, "git", "config", "--get", "core.commentChar").Output(); err == nil && strings.TrimSpace(string(out)) != "" {
			comment = strings.TrimSpace(string(out))
		}
		marker := comment + " ------------------------ >8 ------------------------"
		lines := bytes.SplitAfter(message, []byte("\n"))
		at := 0
		for _, line := range lines {
			if bytes.Equal(bytes.TrimSuffix(line, []byte("\n")), []byte(marker)) {
				message = message[:at]
				break
			}
			at += len(line)
		}
		mode = "whitespace"
	}
	args := []string{"stripspace"}
	if mode == "strip" {
		args = append(args, "--strip-comments")
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(message)
	return cmd.Output()
}

func replaceCommitMessage(path string, message []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("commit message target must be a regular file")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dispat-message-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(message); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
