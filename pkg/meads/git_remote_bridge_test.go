package meads

import (
	"context"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestWazeroGitHTTPBridgeFetchPushAndReject(t *testing.T) {
	root := t.TempDir()
	remoteDir := filepath.Join(root, "remote.git")
	if err := os.Mkdir(remoteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	remoteNative := &ExecGit{Dir: remoteDir}
	if err := remoteNative.Run("init", "--quiet", "--bare", "."); err != nil {
		t.Fatalf("init remote: %v", err)
	}
	if err := remoteNative.Run("config", "http.receivepack", "true"); err != nil {
		t.Fatalf("enable HTTP push: %v", err)
	}

	execPath, err := remoteNative.Output("--exec-path")
	if err != nil {
		t.Fatalf("git --exec-path: %v", err)
	}
	backend := filepath.Join(execPath, "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Fatalf("locate git-http-backend: %v", err)
	}
	backendHandler := &cgi.Handler{
		Path: backend,
		Env: []string{
			"GIT_PROJECT_ROOT=" + root,
			"GIT_HTTP_EXPORT_ALL=1",
		},
	}
	const httpUser, httpPassword = "meads", "bridge-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != httpUser || password != httpPassword {
			w.Header().Set("WWW-Authenticate", `Basic realm="meads bridge test"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		backendHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	remoteURL := server.URL + "/remote.git"
	t.Setenv("MEADS_GIT_HTTP_USERNAME", httpUser)
	t.Setenv("MEADS_GIT_HTTP_PASSWORD", httpPassword)

	producer, producerRefs, _ := newWazeroTestRepo(t)
	configureBridgeRemote(t, producer.Dir, remoteURL)
	first, err := producerRefs.CommitFile(
		"refs/meads/tasks/1", "task.json", []byte(`{"source":"producer-1"}`), ZeroOID, "producer one",
	)
	if err != nil {
		t.Fatalf("producer CommitFile: %v", err)
	}
	out, err := producer.CombinedOutputContext(context.Background(),
		"push", "--porcelain", "origin", RefNamespace+"*:"+RefNamespace+"*")
	if err != nil {
		t.Fatalf("HTTP bridge push: %v\n%s", err, out)
	}
	if !strings.Contains(out, "To "+remoteURL) || !strings.Contains(out, "Done") {
		t.Fatalf("HTTP bridge push output = %q", out)
	}

	listed, err := producer.OutputContext(context.Background(), "ls-remote", "origin", RefNamespace+"*")
	if err != nil {
		t.Fatalf("HTTP bridge ls-remote: %v", err)
	}
	if !strings.Contains(listed, string(first)+"\trefs/meads/tasks/1") {
		t.Fatalf("HTTP bridge ls-remote = %q, want task ref %s", listed, first)
	}

	// Clone adoption supplies an explicit force-wildcard refspec and lands
	// the advertised namespace directly in local refs/meads/*.
	adopter, adopterRefs, _ := newWazeroTestRepo(t)
	configureBridgeRemote(t, adopter.Dir, remoteURL)
	if err := adopter.RunContext(context.Background(),
		"fetch", "origin", "+"+RefNamespace+"*:"+RefNamespace+"*"); err != nil {
		t.Fatalf("HTTP bridge adoption fetch: %v", err)
	}
	if got, err := adopterRefs.ResolveRef("refs/meads/tasks/1"); err != nil || got != first {
		t.Fatalf("adopted ref = %s, %v; want %s", got, err, first)
	}

	consumer, consumerRefs, _ := newWazeroTestRepo(t)
	configureBridgeRemote(t, consumer.Dir, remoteURL)
	if err := consumer.RunContext(context.Background(), "fetch", "origin"); err != nil {
		t.Fatalf("HTTP bridge fetch: %v", err)
	}
	fetchedRef := RemoteTasksRefPrefix + "1"
	if got, err := consumerRefs.ResolveRef(fetchedRef); err != nil || got != first {
		t.Fatalf("fetched ref = %s, %v; want %s", got, err, first)
	}
	content, _, err := consumerRefs.ReadFileAtRef(fetchedRef, "task.json")
	if err != nil || string(content) != `{"source":"producer-1"}` {
		t.Fatalf("fetched content = %q, %v", content, err)
	}

	// Both clones now branch from the same fetched commit. Producer wins the
	// next push; consumer's independent child must receive the same stable
	// porcelain rejection shape Sync uses to classify "fetch first".
	secondProducer, err := producerRefs.CommitFile(
		"refs/meads/tasks/1", "task.json", []byte(`{"source":"producer-2"}`), first, "producer two",
	)
	if err != nil {
		t.Fatalf("producer second CommitFile: %v", err)
	}
	if out, err := producer.CombinedOutputContext(context.Background(),
		"push", "--porcelain", "origin", RefNamespace+"*:"+RefNamespace+"*"); err != nil {
		t.Fatalf("producer second push: %v\n%s", err, out)
	}
	if err := consumerRefs.CompareAndSwap("refs/meads/tasks/1", first, ZeroOID); err != nil {
		t.Fatalf("adopt fetched task locally: %v", err)
	}
	secondConsumer, err := consumerRefs.CommitFile(
		"refs/meads/tasks/1", "task.json", []byte(`{"source":"consumer-2"}`), first, "consumer two",
	)
	if err != nil {
		t.Fatalf("consumer second CommitFile: %v", err)
	}
	out, err = consumer.CombinedOutputContext(context.Background(),
		"push", "--porcelain", "origin", RefNamespace+"*:"+RefNamespace+"*")
	if err == nil {
		t.Fatalf("divergent HTTP bridge push succeeded; output %q", out)
	}
	if !PushRejected(out) || !strings.Contains(out, "[rejected] (fetch first)") {
		t.Fatalf("divergent push output = %q, want porcelain fetch-first rejection", out)
	}
	remoteTip, err := NewRefStore(remoteNative).ResolveRef("refs/meads/tasks/1")
	if err != nil || remoteTip != secondProducer || remoteTip == secondConsumer {
		t.Fatalf("remote tip = %s, %v; want producer %s, not consumer %s", remoteTip, err, secondProducer, secondConsumer)
	}
}

func configureBridgeRemote(t *testing.T, dir, remoteURL string) {
	t.Helper()
	native := &ExecGit{Dir: dir}
	if err := native.Run("remote", "add", "origin", remoteURL); err != nil {
		t.Fatalf("add origin: %v", err)
	}
	if err := native.Run("config", "--add", "remote.origin.fetch", FetchRefspec); err != nil {
		t.Fatalf("add Meads fetch refspec: %v", err)
	}
	wasm := NewWazeroGit(dir)
	t.Cleanup(func() { _ = wasm.Close(context.Background()) })
	result := wasm.remoteBridgeCommand(context.Background(), "remote", "get-url", "origin")
	if !result.handled || result.err != nil || result.stdout != remoteURL {
		t.Fatalf("remote get-url bridge = handled %v, out %q, err %v; want %q", result.handled, result.stdout, result.err, remoteURL)
	}
}

func TestRemoteBridgeCommandSelection(t *testing.T) {
	for _, test := range []struct {
		args    []string
		command string
		ok      bool
	}{
		{args: []string{"fetch", "origin"}, command: "fetch", ok: true},
		{args: []string{"push", "origin", "refs/a:refs/a"}, command: "push", ok: true},
		{args: []string{"ls-remote", "origin", "refs/meads/*"}, command: "ls-remote", ok: true},
		{args: []string{"-c", "http.sslVerify=false", "fetch", "origin"}, ok: false},
		{args: []string{"cat-file", "blob", "HEAD:x"}, command: "cat-file", ok: true},
	} {
		t.Run(fmt.Sprint(test.args), func(t *testing.T) {
			command, _, ok := plainGitCommand(test.args)
			if ok != test.ok || command != test.command {
				t.Fatalf("plainGitCommand(%q) = %q, %v; want %q, %v", test.args, command, ok, test.command, test.ok)
			}
		})
	}
}

func TestWazeroGitSSHBridgeFetchAndPush(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test SSH server launches the platform Git service binaries")
	}
	root := t.TempDir()
	remoteDir := filepath.Join(root, "remote.git")
	if err := os.Mkdir(remoteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	remoteNative := &ExecGit{Dir: remoteDir}
	if err := remoteNative.Run("init", "--quiet", "--bare", "."); err != nil {
		t.Fatalf("init remote: %v", err)
	}

	server := newTestGitSSHServer(t, remoteDir)
	remoteURL := fmt.Sprintf("ssh://git@%s%s", server.addr, remoteDir)
	t.Setenv("MEADS_GIT_SSH_KEY", server.clientKey)
	t.Setenv("SSH_KNOWN_HOSTS", server.knownHosts)

	producer, producerRefs, _ := newWazeroTestRepo(t)
	configureBridgeRemote(t, producer.Dir, remoteURL)
	want, err := producerRefs.CommitFile(
		"refs/meads/tasks/9", "task.json", []byte(`{"transport":"ssh"}`), ZeroOID, "SSH task",
	)
	if err != nil {
		t.Fatalf("CommitFile: %v", err)
	}
	if out, err := producer.CombinedOutputContext(context.Background(),
		"push", "--porcelain", "origin", RefNamespace+"*:"+RefNamespace+"*"); err != nil {
		t.Fatalf("SSH bridge push: %v\n%s", err, out)
	}

	consumer, consumerRefs, _ := newWazeroTestRepo(t)
	configureBridgeRemote(t, consumer.Dir, remoteURL)
	if err := consumer.RunContext(context.Background(), "fetch", "origin"); err != nil {
		t.Fatalf("SSH bridge fetch: %v", err)
	}
	got, err := consumerRefs.ResolveRef(RemoteTasksRefPrefix + "9")
	if err != nil || got != want {
		t.Fatalf("SSH fetched ref = %s, %v; want %s", got, err, want)
	}
	content, _, err := consumerRefs.ReadFileAtRef(RemoteTasksRefPrefix+"9", "task.json")
	if err != nil || string(content) != `{"transport":"ssh"}` {
		t.Fatalf("SSH fetched content = %q, %v", content, err)
	}
}

type testGitSSHServer struct {
	addr       string
	clientKey  string
	knownHosts string
	listener   net.Listener
	wait       sync.WaitGroup
}

func newTestGitSSHServer(t *testing.T, allowedRepo string) *testGitSSHServer {
	t.Helper()
	_, hostPrivate, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatal(err)
	}
	clientPublic, clientPrivate, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSSHKey, err := ssh.NewPublicKey(clientPublic)
	if err != nil {
		t.Fatal(err)
	}
	clientFingerprint := ssh.FingerprintSHA256(clientSSHKey)
	clientDER, err := x509.MarshalPKCS8PrivateKey(clientPrivate)
	if err != nil {
		t.Fatal(err)
	}
	clientKey := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(clientKey, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: clientDER}), 0o600); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{listener.Addr().String()}, hostSigner.PublicKey()) + "\n"
	if err := os.WriteFile(knownHosts, []byte(line), 0o600); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}

	server := &testGitSSHServer{
		addr:       listener.Addr().String(),
		clientKey:  clientKey,
		knownHosts: knownHosts,
		listener:   listener,
	}
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if ssh.FingerprintSHA256(key) != clientFingerprint {
				return nil, errors.New("unauthorized test SSH key")
			}
			return nil, nil
		},
	}
	config.AddHostKey(hostSigner)
	server.wait.Add(1)
	go func() {
		defer server.wait.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			server.wait.Add(1)
			go func() {
				defer server.wait.Done()
				handleTestGitSSHConnection(conn, config, allowedRepo)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		server.wait.Wait()
	})
	return server
}

func handleTestGitSSHConnection(conn net.Conn, config *ssh.ServerConfig, allowedRepo string) {
	serverConn, channels, requests, err := ssh.NewServerConn(conn, config)
	if err != nil {
		_ = conn.Close()
		return
	}
	defer serverConn.Close()
	go ssh.DiscardRequests(requests)
	for next := range channels {
		if next.ChannelType() != "session" {
			_ = next.Reject(ssh.UnknownChannelType, "session required")
			continue
		}
		channel, channelRequests, err := next.Accept()
		if err != nil {
			continue
		}
		go serveTestGitSSHSession(channel, channelRequests, allowedRepo)
	}
}

func serveTestGitSSHSession(channel ssh.Channel, requests <-chan *ssh.Request, allowedRepo string) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "exec" {
			_ = request.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
			_ = request.Reply(false, nil)
			return
		}
		service, repo, ok := parseTestGitSSHCommand(payload.Command)
		if !ok || filepath.Clean(repo) != filepath.Clean(allowedRepo) {
			_ = request.Reply(false, nil)
			return
		}
		_ = request.Reply(true, nil)
		cmd := exec.Command(service, repo)
		cmd.Stdin = channel
		cmd.Stdout = channel
		cmd.Stderr = channel.Stderr()
		err := cmd.Run()
		status := uint32(0)
		if err != nil {
			status = 1
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				status = uint32(exitErr.ExitCode())
			}
		}
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
		return
	}
}

func parseTestGitSSHCommand(command string) (service, repo string, ok bool) {
	for _, candidate := range []string{"git-upload-pack", "git-receive-pack"} {
		prefix := candidate + " '"
		if strings.HasPrefix(command, prefix) && strings.HasSuffix(command, "'") {
			return candidate, strings.TrimSuffix(strings.TrimPrefix(command, prefix), "'"), true
		}
	}
	return "", "", false
}
