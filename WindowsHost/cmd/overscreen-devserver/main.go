// Command overscreen-devserver runs the Windows host's real HTTP/pairing/signaling stack with a
// fake media engine, on any OS. It takes the same flags and prints the same lines as the Swift
// dev server, so Tests/web/*.test.cjs run against both (OVERSCREEN_DEVSERVER=<this binary>).
//
//	overscreen-devserver [--port 8080] [--no-pairing] [--host 0.0.0.0]
//	                     [--tls <dir> [--http-port 8081] [--allow-insecure]] [--media test]
//
// --media test streams an FFmpeg test pattern over real WebRTC (needs ffmpeg on PATH).
// --media native (Windows) streams a real display; add --mirror to stream the main screen
// instead of a virtual one.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/media"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/netinfo"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/pairing"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/server"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/settings"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/tlsid"
)

func main() {
	st := settings.Default()
	st.Port = 8080
	st.Displays = []settings.Display{{ID: 0, Name: "Display 1"}, {ID: 1, Name: "Display 2"}}
	bindHost := "0.0.0.0"
	tlsDir := ""
	httpPort := 0
	mediaMode := ""

	args := os.Args[1:]
	next := func() string {
		if len(args) == 0 {
			return ""
		}
		v := args[0]
		args = args[1:]
		return v
	}
	for len(args) > 0 {
		switch arg := next(); arg {
		case "--port":
			if p, err := strconv.Atoi(next()); err == nil {
				st.Port = p
			}
		case "--no-pairing":
			st.RequirePairing = false
		case "--host":
			bindHost = next()
		case "--tls":
			tlsDir = next()
		case "--http-port":
			httpPort, _ = strconv.Atoi(next())
		case "--media":
			mediaMode = next()
		case "--mirror":
			st.Mode = settings.ModeMirror
		case "--allow-insecure":
			st.AllowInsecureHTTP = true
		default:
			fmt.Fprintf(os.Stderr, "unknown argument %s\n", arg)
			os.Exit(2)
		}
	}

	p := pairing.New(pairing.DefaultConfig(), nil)
	p.OnCodeChange = func(code string) { fmt.Println("pairing code: " + code) }
	ctx := server.NewContext(st, p, "0.1.0-dev", "Overscreen dev server")
	ctx.Hub.OnChange = func(clients []server.ConnectedClient) {
		fmt.Print("clients:")
		for _, c := range clients {
			fmt.Printf(" %s@display%d %s", c.ID, c.DisplayIndex, c.RemoteAddress)
		}
		fmt.Println()
	}
	var engine server.MediaEngine = &server.FakeEngine{}
	if mediaMode != "" {
		var platform media.Platform = &media.TestPlatform{}
		if mediaMode == "native" {
			native, err := nativePlatform()
			if err != nil {
				fail(err)
			}
			platform = native
		}
		e, err := media.NewEngine(platform, st)
		if err != nil {
			fail(err)
		}
		fmt.Println("encoder: " + e.EncoderName())
		engine = e
	}

	scheme := "http"
	transport := server.Plain
	var main *server.HTTPServer
	var err error
	if tlsDir != "" {
		ips := []string{"127.0.0.1"}
		for _, a := range netinfo.IPv4Addresses() {
			ips = append(ips, a.Address)
		}
		id, err := tlsid.LoadOrCreate(tlsDir, "Overscreen dev server", []string{"localhost"}, ips, time.Now())
		if err != nil {
			fail(err)
		}
		fmt.Println("certificate sha256: " + id.Fingerprint)
		scheme, transport = "https", server.TLS
		main, err = server.NewHTTPServer(ctx, engine, transport, id.TLSConfig(), 0)
		if err != nil {
			fail(err)
		}
	} else if main, err = server.NewHTTPServer(ctx, engine, transport, nil, 0); err != nil {
		fail(err)
	}
	port, err := main.Start(bindHost, []int{st.Port})
	if err != nil {
		fail(err)
	}
	if httpPort != 0 && tlsDir != "" {
		t, note := server.RedirectToHTTPS, "(redirects to https)"
		if st.AllowInsecureHTTP {
			t, note = server.Plain, "(full client, unencrypted)"
		}
		plain, _ := server.NewHTTPServer(ctx, engine, t, nil, port)
		bound, err := plain.Start(bindHost, []int{httpPort})
		if err != nil {
			fail(err)
		}
		fmt.Printf("plain http on %d %s\n", bound, note)
	}
	fmt.Printf("listening on %s://%s:%d\n", scheme, bindHost, port)
	if st.RequirePairing {
		fmt.Println("pairing code: " + p.CurrentCode())
	}
	for _, a := range netinfo.IPv4Addresses() {
		fmt.Printf("  %s://%s:%d  (%s)\n", scheme, a.Address, port, a.Interface)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}

func fail(err error) {
	fmt.Printf("failed to start: %v\n", err)
	os.Exit(1)
}
