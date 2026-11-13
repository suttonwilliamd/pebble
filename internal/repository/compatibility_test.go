package repository

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const filesystemBaseline = "61586df728b858f04d39775855a78f230bfeb7a9"

// This harness is compiled WITH the actual frozen PB09 source, never linked
// against the current implementation. Only pre-lock scheduling hooks are added;
// marker checks, lock operations, mutation and recovery code remain the baseline.
const oldRepositoryConsumer = `package repository
import("bufio";"bytes";"context";"fmt";"io";"os";"testing";"github.com/suttonwilliamd/pebble/internal/storev2")
func pb10Wait(){fmt.Println("READY");if _,err:=bufio.NewReader(os.Stdin).ReadString('\n');err!=nil{panic(err)}}
func pb10Prelock(){if os.Getenv("PEBBLE_OLD_PRELOCK")=="1"{pb10Wait()}}
type pb10Reader struct{r io.Reader;first bool}
func(r *pb10Reader)Read(p []byte)(int,error){if !r.first{r.first=true;pb10Wait()};return r.r.Read(p)}
func TestPB10OldConsumer(t *testing.T){
 root:=os.Getenv("PEBBLE_OLD_ROOT");r,err:=Open(root);if err!=nil{t.Fatal(err)}
 action:=os.Getenv("PEBBLE_OLD_ACTION")
 switch action{
 case "held":
  s,err:=storev2.Open(root);if err!=nil{t.Fatal(err)};pb10Wait()
  payload:=[]byte("old consumer must not publish this");id,err:=storev2.NewID(storev2.Chunk,payload);if err!=nil{t.Fatal(err)};header,err:=storev2.Header(storev2.Chunk,uint64(len(payload)));if err!=nil{t.Fatal(err)}
  operations:=[]func()error{
   func()error{return s.Put(context.Background(),id,bytes.NewReader(append(header[:],payload...)))},
   func()error{_,e:=r.Commit("old writer","Fixture","fixture@example.invalid");return e},
   func()error{return r.CreateBranch("old-writer")},func()error{return r.Restore("tracked")},
   func()error{return r.Checkout("before")},func()error{return r.Recover(context.Background())},
  }
  for i,op:=range operations{if err:=op();err==nil{t.Fatalf("old operation %d crossed capability transition",i)}}
  fmt.Println("REJECTED_HELD")
 case "active-store":
  s,err:=storev2.Open(root);if err!=nil{t.Fatal(err)};payload:=[]byte("old active writer")
  id,err:=storev2.NewID(storev2.Chunk,payload);if err!=nil{t.Fatal(err)};header,err:=storev2.Header(storev2.Chunk,uint64(len(payload)));if err!=nil{t.Fatal(err)}
  if err=s.Put(context.Background(),id,&pb10Reader{r:bytes.NewReader(append(header[:],payload...))});err!=nil{t.Fatal(err)}
  fmt.Println("COMPLETED_OLD")
 case "active-worktree":
  r.fault=func(stage,path string)error{if stage=="journal-publish"{pb10Wait()};return nil}
  if err=r.Restore("tracked");err!=nil{t.Fatal(err)};fmt.Println("COMPLETED_OLD")
 case "prelock":
  if err=r.Restore("tracked");err==nil{t.Fatal("old pre-lock repository consumer mutated upgraded root")};fmt.Println("REJECTED_PRELOCK")
 case "pending":
  r.fault=func(stage,path string)error{if stage=="journal-published"{os.Exit(73)};return nil}
  if err=r.Checkout("after");err!=nil{t.Fatal(err)};t.Fatal("old interruption not reached")
 default:t.Fatal("unknown old consumer action",action)
 }
}
`
const oldStoreConsumer = `package storev2
import("bufio";"bytes";"context";"fmt";"os";"testing")
func pb10Prelock(){if os.Getenv("PEBBLE_OLD_PRELOCK")=="1"{fmt.Println("READY");if _,err:=bufio.NewReader(os.Stdin).ReadString('\n');err!=nil{panic(err)}}}
func TestPB10OldConsumer(t *testing.T){s,err:=Open(os.Getenv("PEBBLE_OLD_ROOT"));if err!=nil{t.Fatal(err)};payload:=[]byte("old pre-lock store writer");id,err:=NewID(Chunk,payload);if err!=nil{t.Fatal(err)};header,err:=Header(Chunk,uint64(len(payload)));if err!=nil{t.Fatal(err)};if err=s.Put(context.Background(),id,bytes.NewReader(append(header[:],payload...)));err==nil{t.Fatal("old pre-lock store writer mutated upgraded root")};fmt.Println("REJECTED_PRELOCK")}
`

func fixtureSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			out[name] = "directory"
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			out[name] = "link:" + link
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[name] = "file:" + string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func oldConsumerFixture(t *testing.T) *Repository {
	t.Helper()
	r, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Generated format2 fixture, not an implicit upgrade/downgrade of user data.
	if err = os.WriteFile(filepath.Join(r.root, ".pebble", "format"), []byte("pebble-format 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	write(t, r.root, "tracked", []byte("base bytes"))
	frozenSnapshot(t, r, "base")
	if err = r.CreateBranch("before"); err != nil {
		t.Fatal(err)
	}
	return r
}

type runningOldConsumer struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Reader
	stderr bytes.Buffer
}

func startOldConsumer(t *testing.T, bin, root, action string, prelock bool) *runningOldConsumer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, bin, "-test.run=^TestPB10OldConsumer$", "-test.v")
	cmd.Env = append(os.Environ(), "PEBBLE_OLD_ROOT="+root, "PEBBLE_OLD_ACTION="+action)
	if prelock {
		cmd.Env = append(cmd.Env, "PEBBLE_OLD_PRELOCK=1")
	}
	p := &runningOldConsumer{cmd: cmd}
	cmd.Stderr = &p.stderr
	var err error
	p.input, err = cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p.output = bufio.NewReader(stdout)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for {
		line, err := p.output.ReadString('\n')
		if strings.TrimSpace(line) == "READY" {
			break
		}
		if err != nil {
			_ = cmd.Wait()
			t.Fatalf("old consumer never reached ready: %s %v\n%s", line, err, p.stderr.String())
		}
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return p
}
func (p *runningOldConsumer) resume(t *testing.T, want string) {
	t.Helper()
	if _, err := io.WriteString(p.input, "continue\n"); err != nil {
		t.Fatal(err)
	}
	_ = p.input.Close()
	data, readErr := io.ReadAll(p.output)
	err := p.cmd.Wait()
	if err != nil || readErr != nil || !strings.Contains(string(data), want) {
		t.Fatalf("old consumer: %v %v\n%s\n%s", err, readErr, data, p.stderr.String())
	}
}
func buildOldConsumer(t *testing.T, source, relative, harness, needle string) string {
	t.Helper()
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	suffix := ""
	if runtime.GOOS == "windows" {
		goExe += ".exe"
		suffix = ".exe"
	}
	harnessPath := filepath.Join(source, relative, "pb10_consumer_test.go")
	if err := os.WriteFile(harnessPath, []byte(harness), 0600); err != nil {
		t.Fatal(err)
	}
	productionName := "repository.go"
	if relative == "internal/storev2" {
		productionName = "store.go"
	}
	production := filepath.Join(source, relative, productionName)
	data, err := os.ReadFile(production)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), needle) != 1 {
		t.Fatal("baseline scheduling anchor changed")
	}
	instrumented := strings.Replace(string(data), needle, "pb10Prelock()\n\t"+needle, 1)
	if err = os.WriteFile(production, []byte(instrumented), 0600); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.WriteFile(production, data, 0600); err != nil {
			t.Error(err)
		}
		if err := os.Remove(harnessPath); err != nil {
			t.Error(err)
		}
	}()
	bin := filepath.Join(t.TempDir(), "old-consumer"+suffix)
	cmd := exec.Command(goExe, "test", "-c", "-o", bin, "./"+filepath.ToSlash(relative))
	cmd.Dir = source
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build real baseline consumer: %v\n%s", err, out)
	}
	return bin
}
func TestFilesystemOldConsumers(t *testing.T) {
	source := os.Getenv("PEBBLE_TEST_OLD_SOURCE")
	if source == "" {
		if os.Getenv("PEBBLE_REQUIRE_OLD_CONSUMER") == "1" {
			t.Fatal("required frozen baseline source unavailable")
		}
		t.Skip("real old-consumer proof requires generated frozen baseline source; native CI supplies it explicitly")
	}
	if !strings.HasPrefix(filepath.Base(source), "pebble-pb10-old-") {
		t.Fatal("old consumer source must be a generated temporary fixture")
	}
	baseline, err := os.ReadFile(filepath.Join(source, "BASELINE"))
	if err != nil || strings.TrimSpace(string(baseline)) != filesystemBaseline {
		t.Fatalf("wrong frozen baseline: %q %v", baseline, err)
	}
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	suffix := ""
	if runtime.GOOS == "windows" {
		goExe += ".exe"
		suffix = ".exe"
	}
	oldCLI := filepath.Join(t.TempDir(), "pebble-old"+suffix)
	build := exec.Command(goExe, "build", "-o", oldCLI, "./cmd/pebble")
	build.Dir = source
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build unmodified baseline CLI: %v\n%s", err, out)
	}
	repoBin := buildOldConsumer(t, source, "internal/repository", oldRepositoryConsumer, `unlock, err := processlock.Acquire(filepath.Join(r.root, ".pebble", "worktree.guard"))`)
	storeBin := buildOldConsumer(t, source, "internal/storev2", oldStoreConsumer, `unlock, err := processlock.Acquire(filepath.Join(s.meta, "write.guard"))`)
	for _, action := range []string{"held", "prelock-repository", "prelock-store", "active-worktree", "active-store"} {
		t.Run(action, func(t *testing.T) {
			r := oldConsumerFixture(t)
			write(t, r.root, "tracked", []byte("pre-existing dirty bytes"))
			bin, mode, prelock := repoBin, action, false
			if strings.HasPrefix(action, "prelock-") {
				mode, prelock = "prelock", true
				if action == "prelock-store" {
					bin = storeBin
				}
			}
			p := startOldConsumer(t, bin, r.root, mode, prelock)
			if strings.HasPrefix(action, "active-") {
				if err = r.EnableFilesystem(context.Background()); err == nil {
					t.Fatal("enablement crossed active old operation")
				}
				marker, err := os.ReadFile(filepath.Join(r.root, ".pebble", "format"))
				if err != nil || string(marker) != "pebble-format 2\n" {
					t.Fatalf("marker changed during active owner: %q %v", marker, err)
				}
				p.resume(t, "COMPLETED_OLD")
			} else {
				if err = r.EnableFilesystem(context.Background()); err != nil {
					t.Fatal(err)
				}
				before := fixtureSnapshot(t, r.root)
				want := "REJECTED_PRELOCK"
				if action == "held" {
					want = "REJECTED_HELD"
				}
				p.resume(t, want)
				if after := fixtureSnapshot(t, r.root); !reflect.DeepEqual(before, after) {
					t.Fatal("old open/pre-lock library consumer changed upgraded bytes or metadata")
				}
				for _, args := range [][]string{{"status"}, {"commit", "old-cli"}, {"restore", "tracked"}, {"recover"}, {"branch", "old-cli"}, {"checkout", "before"}} {
					cmd := exec.Command(oldCLI, args...)
					cmd.Dir = r.root
					cmd.Env = append(os.Environ(), "PEBBLE_AUTHOR=Fixture", "PEBBLE_EMAIL=fixture@example.invalid")
					if out, err := cmd.CombinedOutput(); err == nil {
						t.Fatalf("unmodified baseline CLI accepted capability root: %v\n%s", args, out)
					}
				}
				if after := fixtureSnapshot(t, r.root); !reflect.DeepEqual(before, after) {
					t.Fatal("old CLI changed upgraded bytes or metadata")
				}
			}
		})
	}
	t.Run("old-recovery-rejects-new-journal", func(t *testing.T) {
		r := transitionFixture(t)
		if err = os.WriteFile(filepath.Join(r.root, ".pebble", "format"), []byte("pebble-format 2\n"), 0600); err != nil {
			t.Fatal(err)
		}
		stopped := errors.New("published v2 transition intent")
		r.fault = func(stage, path string) error {
			if stage == "journal-published" {
				return stopped
			}
			return nil
		}
		if err = r.Checkout("file"); !errors.Is(err, stopped) {
			t.Fatal(err)
		}
		write(t, r.root, "node/deep/child", []byte("post-interruption tracked edits"))
		write(t, r.root, "node/untracked", []byte("post-interruption untracked bytes"))
		before := fixtureSnapshot(t, r.root)
		cmd := exec.Command(oldCLI, "recover")
		cmd.Dir = r.root
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "unsupported or corrupt worktree journal") {
			t.Fatalf("old recovery failed to reject version before replay: %v\n%s", err, out)
		}
		if after := fixtureSnapshot(t, r.root); !reflect.DeepEqual(before, after) {
			t.Fatal("old recovery mutated new transition/dirty/untracked data")
		}
	})
	t.Run("new-recovery-preserves-real-old-intent", func(t *testing.T) {
		r := oldConsumerFixture(t)
		write(t, r.root, "tracked", []byte("after bytes"))
		frozenSnapshot(t, r, "after")
		if err = r.CreateBranch("after"); err != nil {
			t.Fatal(err)
		}
		if err = r.Checkout("before"); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(repoBin, "-test.run=^TestPB10OldConsumer$")
		cmd.Env = append(os.Environ(), "PEBBLE_OLD_ROOT="+r.root, "PEBBLE_OLD_ACTION=pending")
		out, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 73 {
			t.Fatalf("real old journal interruption: %v\n%s", err, out)
		}
		before := fixtureSnapshot(t, r.root)
		if err = r.EnableFilesystem(context.Background()); err == nil {
			t.Fatal("enablement crossed real old pending intent")
		}
		if after := fixtureSnapshot(t, r.root); !reflect.DeepEqual(before, after) {
			t.Fatal("refused enablement changed old recovery data")
		}
		r, err = Open(r.root)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if err = r.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		data, err := os.ReadFile(filepath.Join(r.root, "tracked"))
		if err != nil || string(data) != "after bytes" {
			t.Fatalf("old intent lost: %q %v", data, err)
		}
		if head, err := r.GetHead(); err != nil || head != "heads/after" {
			t.Fatalf("old intent HEAD lost: %s %v", head, err)
		}
		if _, err = r.Verify(); err != nil {
			t.Fatal(err)
		}
	})
	fmt.Printf("real baseline %s CLI and held/open/active/pre-lock/recovery consumers exercised\n", filesystemBaseline)
}
