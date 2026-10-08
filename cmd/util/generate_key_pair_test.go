/**
*  @file
*  @copyright defined in scdo/LICENSE
 */

package util

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureKeyCmd(args []string) (string, string, error) {
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		return "", "", err
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		return "", "", err
	}
	os.Stdout, os.Stderr = wOut, wErr

	cmd := GetGenerateKeyPairCmd("node")
	cmd.SetArgs(args)
	runErr := cmd.Execute()

	wOut.Close()
	wErr.Close()
	os.Stdout, os.Stderr = oldOut, oldErr

	out, err := io.ReadAll(rOut)
	if err != nil {
		return "", "", err
	}
	errB, err := io.ReadAll(rErr)
	if err != nil {
		return "", "", err
	}
	return string(out), string(errB), runErr
}

func TestKeyCommandWarnsAndPrintsPrivateKey(t *testing.T) {
	stdout, stderr, err := captureKeyCmd([]string{"--shard", "1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "warning: this private key controls the account") {
		t.Fatalf("stderr missing warning: %s", stderr)
	}
	if !strings.Contains(stdout, "private key: 0x") {
		t.Fatalf("stdout missing private key line: %s", stdout)
	}
	if strings.Contains(stderr, "private key: 0x") {
		t.Fatalf("stderr should not contain the key: %s", stderr)
	}
}

func TestKeyCommandWrites0600File(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "wallet.key")
	stdout, stderr, err := captureKeyCmd([]string{"--shard", "1", "--out", out})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stderr, "warning: this private key controls the account") {
		t.Fatalf("stderr missing warning: %s", stderr)
	}
	if strings.Contains(stdout, "private key: 0x") {
		t.Fatalf("stdout printed the private key: %s", stdout)
	}
	if !strings.Contains(stdout, "private key written to") {
		t.Fatalf("stdout missing path: %s", stdout)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(string(body))
	if !strings.HasPrefix(key, "0x") || len(key) != 66 {
		t.Fatalf("file contents = %q", key)
	}
	if strings.Contains(stdout, key) {
		t.Fatal("stdout included the key written to the file")
	}
}

func TestWriteKeyPairStdoutLine(t *testing.T) {
	addr, key, err := GenerateKey(1)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := writeKeyPair(&stdout, &stderr, addr, key, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "private key: ") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}
