package review

// The serve command (standards/CLI.md, "serve FILE", and its optional
// options): serve a review file until stopped.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/exodist/Review-Helper/go/internal/git"
)

const serveUsage = "usage: review-helper serve FILE [--port N | --port-command CMD] [--listen ADDR] [--allow-host NAME]... [--open] [--reviewer NAME]"

// runServe prints the page's URL as soon as the server listens, and runs
// until stopped, so it writes to standard output itself rather than to the
// output Run holds until a command returns.
func runServe(args []string, _ io.Writer) error {
	return serve(args, os.Stdout, os.Stderr)
}

// serveOptions are serve's settings, from options and the environment.
type serveOptions struct {
	file        string
	port        string
	portCommand string
	listen      string
	allowHosts  []string
	open        bool
	reviewer    string
}

func parseServe(args []string) (*serveOptions, error) {
	var so serveOptions
	o := newOptions()
	o.str("port", &so.port)
	o.str("port-command", &so.portCommand)
	o.str("listen", &so.listen)
	o.list("allow-host", &so.allowHosts)
	o.flag("open", &so.open)
	o.str("reviewer", &so.reviewer)
	rest, err := o.parse(args)
	if err != nil {
		return nil, err
	}
	if len(rest) != 1 {
		return nil, errors.New(serveUsage)
	}
	so.file = rest[0]
	if o.given["port"] && o.given["port-command"] {
		return nil, errors.New("give --port or --port-command, not both")
	}
	if o.given["port"] && so.port == "" {
		return nil, errors.New("invalid port ''")
	}
	if !o.given["port"] && !o.given["port-command"] {
		so.portCommand = os.Getenv("REVIEW_HELPER_PORT_COMMAND")
	}
	if !o.given["listen"] {
		so.listen = os.Getenv("REVIEW_HELPER_LISTEN")
	}
	if strings.TrimSpace(so.listen) == "" {
		so.listen = "127.0.0.1"
	}
	if env := os.Getenv("REVIEW_HELPER_ALLOW_HOSTS"); env != "" {
		so.allowHosts = append(so.allowHosts, env)
	}
	var hosts []string
	for _, h := range so.allowHosts {
		for _, name := range strings.Split(h, ",") {
			if name = strings.TrimSpace(name); name != "" {
				hosts = append(hosts, name)
			}
		}
	}
	so.allowHosts = hosts
	return &so, nil
}

func serve(args []string, stdout, stderr io.Writer) error {
	so, err := parseServe(args)
	if err != nil {
		return err
	}
	port, err := so.pickPort(stderr)
	if err != nil {
		return err
	}
	path, err := absReviewPath(so.file)
	if err != nil {
		return err
	}
	repo, err := git.Find(path)
	if err != nil {
		return err
	}
	d, err := OpenDB(path)
	if err != nil {
		return err
	}
	defer d.Close()

	ln, err := net.Listen("tcp", net.JoinHostPort(so.listen, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("cannot listen on %s port %d: %w", so.listen, port, err)
	}
	defer ln.Close()
	bound := ln.Addr().(*net.TCPAddr)
	local := bound.IP.IsLoopback()

	var token string
	hosts := so.allowHosts
	if !local {
		if token, err = NewToken(); err != nil {
			return err
		}
		hosts = append(hosts, networkNames(so.listen, bound.IP)...)
	}
	access := NewAccess(reviewerFor(so.reviewer, repo), token, bound.Port, hosts)
	srv := &http.Server{
		Handler:           NewServer(d, path, repo, access).Handler(),
		ReadHeaderTimeout: 30 * time.Second,
	}

	urls := serveURLs(so.listen, bound)
	if token != "" {
		urls[0] += "?token=" + token // beyond loopback, the first URL is for other machines
	}
	for _, u := range urls {
		fmt.Fprintln(stdout, u)
	}
	if !local {
		fmt.Fprintf(stderr, "review-helper: listening on %s, beyond this machine: anyone with the token URL above can read this review and post feedback, and anyone on this machine can without it; keep the URL private.\n", so.listen)
	}
	if so.open {
		openBrowser(urls[len(urls)-1], stderr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

// pickPort is the port to listen on: --port, else the port command's, else
// 0 (any free port).
func (so *serveOptions) pickPort(stderr io.Writer) (int, error) {
	if so.port != "" || strings.TrimSpace(so.portCommand) == "" {
		if so.port == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(so.port)
		if !allDigits(so.port) || err != nil || n > 65535 {
			return 0, fmt.Errorf("invalid port '%s'", so.port)
		}
		return n, nil
	}
	return runPortCommand(so.portCommand, stderr)
}

// runPortCommand runs the command, split on spaces with no shell, and
// returns the port it prints.
func runPortCommand(command string, stderr io.Writer) (int, error) {
	argv := strings.Fields(command)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return 0, fmt.Errorf("port command '%s' failed with exit code %d", command, exit.ExitCode())
		}
		return 0, fmt.Errorf("cannot run port command '%s': %w", command, err)
	}
	s := strings.TrimSpace(string(out))
	n, err := strconv.Atoi(s)
	if !allDigits(s) || err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("port command '%s' printed '%s', not a port number", command, s)
	}
	return n, nil
}

// reviewerFor is the server's reviewer: as given, else
// REVIEW_HELPER_REVIEWER, else git's user name and email, else the login
// name, else unknown.
func reviewerFor(given string, repo *git.Repo) string {
	for _, r := range []string{given, os.Getenv("REVIEW_HELPER_REVIEWER"), repo.User(), loginName()} {
		if strings.TrimSpace(r) != "" {
			return r
		}
	}
	return "unknown"
}

// serveURLs are the URLs to print: the one to open from another machine,
// then the loopback one when the socket answers on loopback too.
func serveURLs(listen string, bound *net.TCPAddr) []string {
	url := func(host string) string {
		return "http://" + net.JoinHostPort(host, strconv.Itoa(bound.Port)) + "/"
	}
	switch {
	case bound.IP.IsLoopback():
		return []string{url(bound.IP.String())}
	case !bound.IP.IsUnspecified():
		return []string{url(listen)}
	// Go binds a wildcard dual-stack, so the address given says which.
	case net.ParseIP(listen).To4() == nil:
		return []string{url(fqdn()), url("::1")}
	default:
		return []string{url(fqdn()), url("127.0.0.1")}
	}
}

// fqdn is this machine's fully qualified name, else its host name.
func fqdn() string {
	name, err := os.Hostname()
	if err != nil {
		return "localhost"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if cname, err := net.DefaultResolver.LookupCNAME(ctx, name); err == nil && cname != "" {
		return strings.TrimSuffix(cname, ".")
	}
	return name
}

// networkNames are the names a browser elsewhere may put in the Host
// header: the address given and the one bound (unless a wildcard), this
// machine's host name and FQDN and the addresses they resolve to, and the
// addresses outgoing traffic leaves from. Other interfaces' addresses need
// --allow-host.
func networkNames(listen string, bound net.IP) []string {
	var names []string
	if host, err := os.Hostname(); err == nil {
		names = append(names, host)
	}
	names = append(names, fqdn())
	hosts := append([]string{}, names...)
	for _, h := range []string{listen, bound.String()} {
		if ip := net.ParseIP(h); ip == nil || !ip.IsUnspecified() {
			hosts = append(hosts, h)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, n := range names {
		addrs, err := net.DefaultResolver.LookupHost(ctx, n)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			a, _, _ = strings.Cut(a, "%")
			hosts = append(hosts, a)
		}
	}
	// Connecting a UDP socket picks a route without sending anything.
	for _, peer := range []string{"192.0.2.1:9", "[2001:db8::1]:9"} {
		if c, err := net.Dial("udp", peer); err == nil {
			if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
				hosts = append(hosts, a.IP.String())
			}
			c.Close()
		}
	}
	return hosts
}

// openBrowser opens the URL with the desktop's opener on Linux and macOS,
// and does nothing elsewhere. Some openers wait for the browser, which
// waits for the server, so nothing waits on the opener but a goroutine
// that reaps it.
func openBrowser(url string, stderr io.Writer) {
	var opener string
	switch runtime.GOOS {
	case "linux":
		opener = "xdg-open"
	case "darwin":
		opener = "open"
	default:
		return
	}
	cmd := exec.Command(opener, url)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "Could not run '%s'; open the URL by hand.\n", opener)
		return
	}
	go cmd.Wait()
}
