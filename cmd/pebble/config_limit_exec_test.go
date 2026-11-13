package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/suttonwilliamd/pebble/internal/storev2"
)

func TestIdentityConfigSerializedLimitCLI(t *testing.T) {
	const limit = 64 << 10 // Documented complete-file consumer bound.
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	binary := filepath.Join(t.TempDir(), "pebble"+suffix)
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"+suffix), "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build actual CLI: %v\n%s", err, output)
	}

	for _, scope := range []string{"local", "global"} {
		for _, scenario := range []string{"plain-boundary", "escaped-boundary", "cumulative", "single-escaped"} {
			t.Run(scope+"/"+scenario, func(t *testing.T) {
				root := t.TempDir()
				env := isolatedIdentityEnv(t)
				home := ""
				for _, entry := range env {
					if strings.HasPrefix(entry, "HOME=") {
						home = strings.TrimPrefix(entry, "HOME=")
					}
				}
				if home == "" {
					t.Fatal("fixture HOME unavailable")
				}
				invoke := func(directory string, success bool, args ...string) string {
					t.Helper()
					command := exec.Command(binary, args...)
					command.Dir, command.Env = directory, env
					output, err := command.CombinedOutput()
					if (err == nil) != success {
						t.Fatalf("CLI %s %v: %v\n%s", directory, args, err, output)
					}
					return string(output)
				}
				config := func(success bool, key string, value ...string) string {
					t.Helper()
					args := []string{"config", "--" + scope, key}
					args = append(args, value...)
					return invoke(root, success, args...)
				}
				invoke(root, true, "init")
				path := filepath.Join(root, ".pebble", "config.json")
				consumer := root
				if scope == "global" {
					path = filepath.Join(home, ".pebbleconfig")
					// A global rejection must not poison a different repository's identity.
					consumer = t.TempDir()
					invoke(consumer, true, "init")
				}
				values := map[string]string{"user.email": "fixture@example.invalid", "user.name": "Small Fixture"}
				if scenario == "plain-boundary" || scenario == "escaped-boundary" {
					values["user.name"] = strings.Repeat("U", 21000)
					values["author.name"] = strings.Repeat("A", 21000)
				} else if scenario == "cumulative" {
					values["user.name"] = strings.Repeat("U", 24000)
					values["author.name"] = strings.Repeat("A", 24000)
				}
				for _, key := range []string{"user.email", "user.name", "author.name"} {
					if value, ok := values[key]; ok {
						config(true, key, value)
					}
				}
				readBytes := func() []byte {
					t.Helper()
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					return data
				}
				// Model JSON's escaping and final newline, not raw identity string length.
				serializedSize := func(candidate map[string]string) int {
					t.Helper()
					data, err := json.MarshalIndent(candidate, "", "  ")
					if err != nil {
						t.Fatal(err)
					}
					return len(data) + 1
				}
				reject := func(key, value string) {
					t.Helper()
					previous := readBytes()
					config(false, key, value)
					if got := readBytes(); !bytes.Equal(got, previous) {
						t.Fatal("rejected update replaced the previous usable configuration")
					}
					if got := config(true, "user.name"); got != values["user.name"]+"\n" {
						t.Fatal("previous config cannot be read after rejection")
					}
				}
				switch scenario {
				case "plain-boundary", "escaped-boundary":
					values["committer.name"] = ""
					overhead := serializedSize(values)
					atSize := func(size int) string {
						budget := size - overhead
						if scenario == "escaped-boundary" {
							return strings.Repeat("<", budget/6) + strings.Repeat("Z", budget%6)
						}
						return strings.Repeat("Z", budget)
					}
					for _, size := range []int{limit - 1, limit} {
						value := atSize(size)
						values["committer.name"] = value
						if serializedSize(values) != size {
							t.Fatal("fixture did not reach final serialized boundary")
						}
						config(true, "committer.name", value)
						if got := len(readBytes()); got != size {
							t.Fatalf("accepted serialized size %d, want %d including newline", got, size)
						}
					}
					above := atSize(limit + 1)
					candidate := make(map[string]string, len(values))
					for key, value := range values {
						candidate[key] = value
					}
					candidate["committer.name"] = above
					if serializedSize(candidate) != limit+1 {
						t.Fatal("fixture did not exceed the final bound by one byte")
					}
					reject("committer.name", above)
					if got := config(true, "committer.name"); got != values["committer.name"]+"\n" {
						t.Fatal("failed update changed exact-boundary value")
					}
				case "cumulative":
					candidate := map[string]string{"user.name": values["user.name"], "user.email": values["user.email"], "author.name": values["author.name"], "committer.name": strings.Repeat("C", 24000)}
					if serializedSize(candidate) <= limit {
						t.Fatal("fixture cumulative map fits unexpectedly")
					}
					reject("committer.name", candidate["committer.name"])
				case "single-escaped":
					// One individually valid short value expands past 64 KiB in JSON.
					escaped := strings.Repeat("<", 11000)
					candidate := map[string]string{"user.name": escaped, "user.email": values["user.email"]}
					if len(escaped) >= limit || serializedSize(candidate) <= limit {
						t.Fatal("fixture does not exercise JSON expansion")
					}
					reject("user.name", escaped)
				}
				commit := func(revision byte) {
					t.Helper()
					if err := os.WriteFile(filepath.Join(consumer, "revision"), []byte{revision, 0, 255, '\r', '\n'}, 0644); err != nil {
						t.Fatal(err)
					}
					invoke(consumer, true, "add", "revision")
					invoke(consumer, true, "commit", "configuration remains usable")
					store, err := storev2.Open(consumer)
					if err != nil {
						t.Fatal(err)
					}
					id, err := store.GetRef(context.Background(), "heads/main")
					if err != nil {
						t.Fatal(err)
					}
					metadata, err := store.ReadCommit(context.Background(), id)
					if err != nil {
						t.Fatal(err)
					}
					author, committer := values["author.name"], values["committer.name"]
					if author == "" {
						author = values["user.name"]
					}
					if committer == "" {
						committer = values["user.name"]
					}
					if metadata.Author != author || metadata.Committer != committer || metadata.Email != values["user.email"] || metadata.CommitterEmail != values["user.email"] {
						t.Fatal("rejected configuration update changed resolved commit identity")
					}
				}
				// Before repair, exact previous bytes still resolve real commit provenance.
				commit(1)
				config(true, "committer.name", "Repaired Fixture")
				values["committer.name"] = "Repaired Fixture"
				if got := config(true, "committer.name"); got != "Repaired Fixture\n" {
					t.Fatal("subsequent CLI update/read failed")
				}
				commit(2)
			})
		}
	}
}
