package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

type Proxy struct {
	transcoders map[string]Transcoder
	ml          *mitmListener
	ReadCount   uint64
	WriteCount  uint64
	AdCount     uint64
	VCount      uint64
	user        string
	pass        string
	host        string
	cert        string
}

var Qjpeg int

type Transcoder interface {
	Transcode(*ResponseWriter, *ResponseReader, http.Header) error
}

func New(host string, cert string) *Proxy {
	p := &Proxy{
		transcoders: make(map[string]Transcoder),
		ml:          nil,
		host:        host,
		cert:        cert,
	}
	return p
}

func (p *Proxy) EnableMitm(ca, key string) error {
	cf, err := newCertFaker(ca, key)
	if err != nil {
		return err
	}

	var config *tls.Config
	if p.cert != "" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			return err
		}
		pem, err := ioutil.ReadFile(p.cert)
		if err != nil {
			return err
		}
		ok := roots.AppendCertsFromPEM([]byte(pem))
		if !ok {
			return errors.New("failed to parse root certificate")
		}
		config = &tls.Config{RootCAs: roots}
	}
	p.ml = newMitmListener(cf, config)
	go http.Serve(p.ml, p)
	return nil
}

func (p *Proxy) SetAuthentication(user, pass string) {
	p.user = user
	p.pass = pass
}

func (p *Proxy) AddTranscoder(contentType string, transcoder Transcoder) {
	p.transcoders[contentType] = transcoder
}

func (p *Proxy) Start(host string) error {
	sv := &http.Server{
        	Addr:              host,
	        Handler:           p,
        	ReadTimeout:       120*time.Second,
	        ReadHeaderTimeout: 30*time.Second,
        	WriteTimeout:      120*time.Second,
	        IdleTimeout:       240*time.Second,
        }
	return sv.ListenAndServe()
}

func (p *Proxy) StartTLS(host, cert, key string) error {
	sv := &http.Server{
                Addr:              host,
                Handler:           p,
                ReadTimeout:       120*time.Second,
                ReadHeaderTimeout: 30*time.Second,
                WriteTimeout:      120*time.Second,
                IdleTimeout:       240*time.Second,
        }
	return sv.ListenAndServeTLS(cert, key)
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
        host := r.URL.Host
        if host == "" {
                host = r.Host
        }
	requestip := strings.Split(host, ":")
	rip,ierr := net.LookupIP(requestip[0])
	if ierr == nil {
	if rip[0].String() == "::1" || rip[0].String() == "0.0.0.0" {
		log.Printf("AD BLOCK %s", r.Host)
		atomic.AddUint64(&p.AdCount, 1)
		w.WriteHeader(http.StatusNotFound)
		//w.Header().Set("Content-Type", "text/html")
		//io.WriteString(w, fmt.Sprintf(`<html><title>Compy AD Block</title></head><body><h1>Compy AD Block</h1>%s</body></html>`, r.URL))
		return
	}
	}
	log.Printf("serving request: %s", r.URL)
	if err := p.handle(w, r, host); err != nil {
		log.Printf("%s while serving request: %s", err, r.URL)
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, fmt.Sprintf(`<html><title>Compy Proxy Error</title></head><body><h1>Compy Proxy error while serving request</h1>%s<br>%s</body></html>`, err, r.URL))
	}
}

func (p *Proxy) checkHttpBasicAuth(auth string) bool {
	prefix := "Basic "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(auth[len(prefix):])
	if err != nil {
		return false
	}
	values := strings.SplitN(string(decoded), ":", 2)
	if len(values) != 2 || values[0] != p.user || values[1] != p.pass {
		return false
	}
	return true
}

func (p *Proxy) handle(w http.ResponseWriter, r *http.Request, host string) error {
	if p.user != "" {
		if !p.checkHttpBasicAuth(r.Header.Get("Proxy-Authorization")) {
			w.Header().Set("WWW-Authenticate", "Basic realm=\"Compy\"")
			w.WriteHeader(http.StatusProxyAuthRequired)
			return nil
		}
	}
	if r.Method == "CONNECT" {
		return p.handleConnect(w, r)
	}
	if host == "vps.greenridge" {
		return p.handleLocalRequest(w, r)
	}

	resp, err := forward(r)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("error forwarding request: %s", err)
	}
	defer resp.Body.Close()
	rw := newResponseWriter(w)
	rr := newResponseReader(resp)
	if _, merr := os.Stat("/run/mobiledata"); merr == nil || Qjpeg == 10 {
		err = p.proxyResponse(rw, rr, r.Header, host)
		read := rr.counter.Count()
	        written := rw.rw.Count()
		if err == nil {
         	       log.Printf("transcoded: %d -> %d (%3.1f%%)", read, written, float64(written)/float64(read)*100)
                	atomic.AddUint64(&p.ReadCount, read)
	                atomic.AddUint64(&p.WriteCount, written)
        	}
		if err != nil && read == 0 {
                	rw.ReadFrom(rr)
	                log.Printf("Pass zero payload")
        	        return nil
        	}
	} else {
		rw.takeHeaders(rr)
                rw.ReadFrom(rr)
	}
	return err
}

func (p *Proxy) handleLocalRequest(w http.ResponseWriter, r *http.Request) error {
	if r.Method == "GET" && (r.URL.Path == "" || r.URL.Path == "/") {
		w.Header().Set("Content-Type", "text/html")
		read := atomic.LoadUint64(&p.ReadCount)
		written := atomic.LoadUint64(&p.WriteCount)
		ads := atomic.LoadUint64(&p.AdCount)
		vids := atomic.LoadUint64(&p.VCount)
		pmode := ""
		if _, err := os.Stat("/run/mobiledata"); err == nil || Qjpeg == 10 {
			pmode = "Compression Mode, Mobile Data "
		} else {
			pmode = "No Compression, Pass through Mode "
		}
		io.WriteString(w, fmt.Sprintf(`<html><head><title>Compy</title></head><body><h1>Compy</h1><ul>
<li>%s%d</li>
<li>Total Compressed: %dKb --> %dKb (%3.1f%%)</li>
<li>Ads Blocked: %d</li>
<li>Videos Blocked: %d</li>
<li><a href="/cacert">CA cert</a></li>
<li><a href="https://github.com/barnacs/compy">GitHub</a></li>
</ul></body></html>`, pmode, Qjpeg, read/1024, written/1024, float64(written)/float64(read)*100, ads, vids))
		return nil
	} else if r.Method == "GET" && r.URL.Path == "/cacert" {
		if p.cert == "" {
			http.NotFound(w, r)
			return nil
		}
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		http.ServeFile(w, r, p.cert)
		return nil
	} else {
		w.WriteHeader(http.StatusNotImplemented)
		return nil
	}
}

func forward(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme == "" {
		if r.TLS != nil && r.TLS.ServerName == r.Host {
			r.URL.Scheme = "https"
		} else {
			r.URL.Scheme = "http"
		}
	}
	if r.URL.Host == "" {
		r.URL.Host = r.Host
	}
	r.RequestURI = ""
	return http.DefaultTransport.RoundTrip(r)
}

func (p *Proxy) proxyResponse(w *ResponseWriter, r *ResponseReader, headers http.Header, url string) error {
	transcoder, found := p.transcoders[r.ContentType()]
	if found {
		w.takeHeaders(r)
                w.setChunked()
                if err := transcoder.Transcode(w, r, headers); err != nil {
                        return fmt.Errorf("transcoding error: %s", err)
                } else {
                        return nil
                }
	} else {
		if strings.HasPrefix(r.ContentType(), "video/") || strings.HasPrefix(r.ContentType(), "audio/"){
			if !strings.HasPrefix(url, "88.80.185.64"){
				log.Printf("BLOCKING AUDIO/VIDEO %s", url)
				w.WriteHeader(http.StatusNotFound)
				//w.Header().Set("Content-Type", "text/html")
				//io.WriteString(w, fmt.Sprintf(`<html><title>Compy Video Block</title></head><body><h1>Compy Video Block</h1>%s</body></html>`,url))
				//atomic.AddUint64(&p.VCount, 1);w.takeHeaders(r);w.Header().Set("Content-Type", "video/mp4");w.Header().Set("Content-Length", "4343");w.Header().Set("Content-Range", "bytes 0-4342/4343");fr, err := os.Open("video.mp4");if err != nil {;return nil;};defer fr.Close();io.Copy(w, fr)
				return nil
			} else {
				w.takeHeaders(r)
				w.setChunked()
				return w.ReadFrom(r)
			}
		} else {
			w.takeHeaders(r)
	                w.setChunked()
        	        if r.ContentType() == "application/octet-stream" || r.ContentType() == "application/zip" || r.ContentType() ==  "application/x-gzip" || r.ContentType() ==  "application/x-bzip" || r.ContentType() ==  "application/x-bzip2" || r.ContentType() ==  "application/x-7z-compressed" || r.ContentType() ==  "application/x-rar-compressed" {
                	        log.Printf("SKIP GZIP  %s", r.ContentType())
                        	return w.ReadFrom(r)
	                } else {
        	                log.Printf("GZIP  %s", r.ContentType())
                	        transcoder1, found := p.transcoders["comp/deflate"]
				if found {
					if err := transcoder1.Transcode(w, r, headers); err != nil {
        	                	        return fmt.Errorf("deflate error: %s", err)
	        	                }
        	        	}
			}
		}
	}
	return nil
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) error {
	if r.Header.Get("Proxy-Connection") == "Keep-Alive" || strings.HasPrefix(r.Header.Get("User-Agent"), "WhatsApp") || strings.HasPrefix(r.Header.Get("User-Agent"), "grpc-java"){
		log.Printf("Tunnel through: %s",  r.Header)
		handleTunneling(w, r)
		return nil
	}
	if p.ml == nil {
		return fmt.Errorf("CONNECT received but mitm is not enabled")
	}

	w.WriteHeader(http.StatusOK)
	var conn net.Conn
	if h, ok := w.(http.Hijacker); ok {
		conn, _, _ = h.Hijack()
	} else {

		fw := w.(FlushWriter)
		fw.Flush()
		mconn := newMitmConn(fw, r.Body, r.RemoteAddr)
		conn = mconn
		defer func() {
			<-mconn.closed
		}()
	}
	sconn, err := p.ml.Serve(conn, r.Host)
	if err != nil {
		conn.Close()
		return err
	}
	sconn.Close()
	return nil
}

func handleTunneling(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		log.Printf("Method not allowed %s", r.Method)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	destConn, err := net.DialTimeout("tcp", r.Host, 30*time.Second)
	if err != nil {
		log.Printf("Destination dial failed %s", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		log.Printf("Hijacking not supported")
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		log.Printf("Hijacking failed %s", err)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	now := time.Now()
	clientConn.SetReadDeadline(now.Add(30*time.Second))
	clientConn.SetWriteDeadline(now.Add(30*time.Second))
	destConn.SetReadDeadline(now.Add(30*time.Second))
	destConn.SetWriteDeadline(now.Add(30*time.Second))

	go transfer(destConn, clientConn)
	go transfer(clientConn, destConn)
}

func transfer(dest io.WriteCloser, src io.ReadCloser) {
	defer func() { _ = dest.Close() }()
	defer func() { _ = src.Close() }()
	_, _ = io.Copy(dest, src)
}
