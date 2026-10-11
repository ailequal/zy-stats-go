// Command fakemodem runs the in-process Zyxel fake on a fixed port so the real
// binary can be exercised end to end without a device:
//
//	just fakemodem
//	just go-run --server-url http://127.0.0.1:8080 --username admin --password q1w2e3r4 --interval 5
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ailequal/zy-stats-go/internal/zyxel/zyxeltest"
)

func main() {
	os.Exit(run())
}

func run() int {
	var o zyxeltest.Options
	var cellwanPath string
	flag.StringVar(&o.Addr, "addr", "127.0.0.1:8080", "listen address")
	flag.StringVar(&o.Username, "username", "admin", "accepted login user")
	flag.StringVar(&o.Password, "password", "q1w2e3r4", "accepted login password")
	flag.StringVar(&cellwanPath, "cellwan", "testdata/cellwan/fwa505-V1.70.json", "cellwan_status JSON fixture to serve")
	flag.StringVar(&o.RSAPublicKey, "rsa-key", "", `RSAPublicKey value to serve (default "None")`)
	flag.BoolVar(&o.SessionKeyOnDAL, "sessionkey-on-dal", false, "require &sessionkey= on DAL reads")
	flag.IntVar(&o.MaxSessions, "max-sessions", 0, "concurrent session limit (0 = unlimited)")
	flag.BoolVar(&o.RotateSessionKey, "rotate-sessionkey", false, "rotate the sessionkey on every DAL response")
	flag.IntVar(&o.ExpiredStatus, "expired-status", 401, "status for expired sessions: 401, 403, 302 or 504")
	flag.BoolVar(&o.TLS, "tls", false, "serve https with a self-signed certificate")
	flag.Parse()

	cellwan, err := os.ReadFile(cellwanPath) //nolint:gosec // path is a CLI flag of a dev tool
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakemodem: read fixture: %v\n", err)
		return 2
	}
	o.Cellwan = cellwan

	s, err := zyxeltest.Start(o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakemodem: %v\n", err)
		return 1
	}
	defer s.Close()

	fmt.Printf("fakemodem listening on %s (user %q, fixture %s)\n", s.URL, o.Username, cellwanPath)
	fmt.Printf("try: just run --server-url %s --username %s --password %s --interval 5\n", s.URL, o.Username, o.Password)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	fmt.Println("\nfakemodem: shutting down")
	return 0
}
