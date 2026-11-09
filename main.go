package main

// WR854GVR Yedek Al — Aidata WR854GVR (RTL9607F) stok firmware yedek aracı.
//
// Ne yapar:
//   1. Web arayüzü kullanıcı adı/şifresini sorar
//   2. Yedeğin kaydedileceği klasörü sorar
//   3. Cihaz erişimini ve portları kontrol eder
//   4. Stok firmware'in firmware-yükleme ucundan (formUpload) root olarak
//      çalışan salt-okunur bir nanddump payload'ı gönderir
//   5. Cihazdan gelen NAND dökümünü TCP üzerinden bu makineye yazar
//
// ÖNEMLİ — bu araç flash'a YAZMAZ:
//   Payload bilerek `exit 1` ile biter. Stok `configd` başarı dalını
//   (`if (local_4 == 0)`) yalnızca script 0 dönerse işletir; o dal
//   sw_updater/sw_tryactive boot env değişkenlerini yazar ve cihazı
//   yeniden başlatır. exit 1 ile ne env yazılır ne reboot olur.
//   Döküm sırasında nanddump salt-okunur çalışır (-o OOB dahil).
//
// Arayüz herhangi bir üçüncü taraf kütüphane kullanmaz; yalnız stdlib.

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	appName    = "WR854GVR Yedek Al"
	appVersion = "1.0"
	defaultIP  = "192.168.1.1"

	// Cihaz payload tarafındaki sabitlerle eşleşir.
	portBase = 9101
)

// partition, cihazın /proc/mtd haritasından (keşifle doğrulandı).
type partition struct {
	index int
	name  string
	size  int64 // bayt, ham bölüm boyutu (OOB hariç)
}

var partitions = []partition{
	{0, "boot", 0x00200000},
	{1, "env", 0x00040000},
	{2, "env2", 0x00040000},
	{3, "static_conf", 0x00040000},
	{4, "ubi_device", 0x1f5c0000},
}

func (p partition) fileName() string {
	return fmt.Sprintf("mtd%d_%s.bin", p.index, p.name)
}

// ---------------------------------------------------------------- terminal

var stdin = bufio.NewReader(os.Stdin)

// ask prompts and returns trimmed input, falling back to def when empty.
func ask(prompt, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", prompt, def)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return def
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// askPassword reads a password without echoing it on Windows or Unix.
func askPassword(prompt string) (string, error) {
	fmt.Printf("%s: ", prompt)
	pw, err := readPasswordNoEcho()
	fmt.Println()
	return pw, err
}

func confirm(prompt string) bool {
	a := strings.ToLower(ask(prompt+" (e/h)", "e"))
	return a == "e" || a == "evet" || a == "y" || a == "yes"
}

// ---------------------------------------------------------------- device

type device struct {
	ip   string
	user string
	pass string
	http *http.Client
}

func newDevice(ip, user, pass string) *device {
	return &device{
		ip:   ip,
		user: user,
		pass: pass,
		http: &http.Client{
			Timeout: 120 * time.Second,
			// Boa cihazı yönlendirmeleri kendisi veriyor; biz elle izliyoruz.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (d *device) url(path string) string {
	return "http://" + d.ip + path
}

// login establishes the Boa session.
//
// Boa has no session cookie: the session is keyed by the client's source
// address. Do NOT rely on HTTP Basic here — the box answers 200 to a Basic
// request carrying any password at all, so it proves nothing. The only
// real credential check is this formLogin POST, which answers 400 for a
// wrong username/password and 301 (to /) for a good one.
func (d *device) login() error {
	form := strings.NewReader(
		"challenge=&username=" + urlEncode(d.user) +
			// base64 padding '=' must itself be percent-encoded
			"&encodePassword=" + urlEncode(base64Encode(d.pass)) +
			"&submit-url=%2Fadmin%2Flogin.asp" +
			"&postSecurityFlag=30011" +
			"&save=" + urlEncode("Giriş Yap"))

	req, err := http.NewRequest("POST", d.url("/boaform/admin/formLogin"), form)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", d.url("/admin/login.asp"))

	resp, err := d.http.Do(req)
	if err != nil {
		return fmt.Errorf("login isteği başarısız: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	switch resp.StatusCode {
	case 200, 301, 302, 303:
		// 301/302/303 => fresh login redirected to /. 200 => the box
		// re-rendered or the session was already live. Both are success;
		// verifySession below does the real proof.
		return nil
	case 400, 401:
		return errors.New("kullanıcı adı veya şifre hatalı")
	default:
		return fmt.Errorf("login beklenmeyen yanıt: HTTP %d", resp.StatusCode)
	}
}

// verifySession proves we can actually reach the upgrade endpoint before we
// bother uploading anything.
func (d *device) verifySession() error {
	resp, err := d.http.Get(d.url("/admin/upgrade.asp"))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode != 200 {
		return fmt.Errorf("yükleme sayfasına erişilemedi (HTTP %d)", resp.StatusCode)
	}
	// A logged-out Boa redirects to the login page; a real page has the form.
	if !bytes.Contains(body, []byte("formUpload")) {
		return errors.New("oturum açılamadı (yükleme formu görünmüyor)")
	}
	return nil
}

// reachable reports whether the device answers a login-page GET.
func (d *device) reachable() error {
	c := &http.Client{Timeout: 6 * time.Second}
	resp, err := c.Get(d.url("/admin/login.asp"))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

// portFree reports whether a local TCP port can be bound.
func portFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// localIPFor returns the local address that routes toward the device, so the
// payload can call back to us even if the bench has several interfaces.
func localIPFor(deviceIP string) (string, error) {
	c, err := net.Dial("udp", net.JoinHostPort(deviceIP, "80"))
	if err != nil {
		return "", err
	}
	defer c.Close()
	local := c.LocalAddr().(*net.UDPAddr)
	ip := local.IP
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
		return "", fmt.Errorf("cihaza giden yerel arayüz bulunamadı")
	}
	return ip.String(), nil
}

// ---------------------------------------------------------------- payload

// payloadScript builds the read-only dumper. Every partition is streamed to
// its own host port so no framing or size guess is needed, and the script
// always ends with `exit 1` (see the file comment at the top).
func payloadScript(host string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Salt-okunur NAND yedek payload'ı. Flash/UBI/env yazımı ve reboot YOK.\n")
	b.WriteString("# exit 1: configd'nin başarı dalı (env yazımı + reboot) çalışmasın.\n")
	b.WriteString("HOST=" + host + "\n\n")
	for _, p := range partitions {
		port := portBase + p.index
		fmt.Fprintf(&b, "/bin/nanddump -a -o --bb=dumpbad -q /dev/mtd%d | /bin/nc $HOST %d\n",
			p.index, port)
	}
	b.WriteString("\nexit 1\n")
	return b.String()
}

// buildTar makes the split-image tarball the stock updater expects:
// fwu_split.sh plus md5.txt whose single line is the md5 of that script.
// Stock validation is a self-checksum only, so this is what makes it pass.
func buildTar(script string) ([]byte, error) {
	var buf bytes.Buffer
	tw := newTarWriter(&buf)

	sum := fmt.Sprintf("%x", md5.Sum([]byte(script)))
	md5line := sum + "  fwu_split.sh\n"

	if err := tw.writeFile("fwu_split.sh", []byte(script), 0755); err != nil {
		return nil, err
	}
	if err := tw.writeFile("md5.txt", []byte(md5line), 0644); err != nil {
		return nil, err
	}
	if err := tw.close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// upload POSTs the tarball to the stock firmware-upgrade endpoint.
func (d *device) upload(tarBytes []byte) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("binary", "firmware.bin")
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(tarBytes); err != nil {
		return "", err
	}
	mw.WriteField("submit-url", "/admin/upgrade.asp")
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", d.url("/boaform/admin/formUpload"), &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Referer", d.url("/admin/upgrade.asp"))

	resp, err := d.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("yükleme başarısız: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))

	// NOTE: "Geçersiz resim dosyası" is the EXPECTED reply. Our payload
	// ends with exit 1, so configd reports failure even though the dump
	// ran. Do not treat it as an error here.
	return htmlText(string(raw)), nil
}

// ---------------------------------------------------------------- receiver

type rxResult struct {
	p    partition
	path string
	n    int64
	err  error
}

// receiveAll listens on every partition port and writes each stream to disk.
// It returns only after all listeners are done or the deadline passes.
func receiveAll(outDir, host string, timeout time.Duration, progress func(partition, int64)) ([]rxResult, error) {
	type listener struct {
		p  partition
		ln net.Listener
	}

	ls := make([]listener, 0, len(partitions))
	for _, p := range partitions {
		addr := fmt.Sprintf("0.0.0.0:%d", portBase+p.index)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			for _, x := range ls {
				x.ln.Close()
			}
			return nil, fmt.Errorf("%s dinlenemedi: %w (port başka bir programda olabilir)", addr, err)
		}
		ls = append(ls, listener{p, ln})
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []rxResult
	)

	deadline := time.Now().Add(timeout)

	for _, l := range ls {
		wg.Add(1)
		go func(l listener) {
			defer wg.Done()
			path := filepath.Join(outDir, l.p.fileName())

			if tl, ok := l.ln.(*net.TCPListener); ok {
				tl.SetDeadline(time.Now().Add(timeout))
			}
			conn, err := l.ln.Accept()
			if err != nil {
				mu.Lock()
				results = append(results, rxResult{p: l.p, path: path, err: err})
				mu.Unlock()
				return
			}
			defer conn.Close()
			if tc, ok := conn.(*net.TCPConn); ok {
				tc.SetReadDeadline(deadline)
			}

			f, err := os.Create(path)
			if err != nil {
				mu.Lock()
				results = append(results, rxResult{p: l.p, path: path, err: err})
				mu.Unlock()
				return
			}
			defer f.Close()

			// Copy with progress, flushing to disk as we go.
			buf := make([]byte, 256*1024)
			var total int64
			for {
				n, rerr := conn.Read(buf)
				if n > 0 {
					if _, werr := f.Write(buf[:n]); werr != nil {
						mu.Lock()
						results = append(results, rxResult{p: l.p, path: path, n: total, err: werr})
						mu.Unlock()
						return
					}
					total += int64(n)
					progress(l.p, total)
				}
				if rerr != nil {
					if rerr == io.EOF {
						rerr = nil
					}
					mu.Lock()
					results = append(results, rxResult{p: l.p, path: path, n: total, err: rerr})
					mu.Unlock()
					return
				}
			}
		}(l)
	}

	wg.Wait()
	for _, l := range ls {
		l.ln.Close()
	}

	sort.Slice(results, func(i, j int) bool { return results[i].p.index < results[j].p.index })
	return results, nil
}

// ---------------------------------------------------------------- helpers

func htmlText(s string) string {
	// strip tags crudely; enough to surface the device's own message
	out := make([]rune, 0, len(s))
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				out = append(out, r)
			}
		}
	}
	return strings.TrimSpace(html.UnescapeString(string(out)))
}

func base64Encode(s string) string { return b64.EncodeToString([]byte(s)) }

func urlEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

func ensureDir(p string) error {
	if p == "" {
		return errors.New("boş klasör yolu")
	}
	if err := os.MkdirAll(p, 0755); err != nil {
		return err
	}
	// verify writability
	f, err := os.CreateTemp(p, ".wr854test")
	if err != nil {
		return fmt.Errorf("klasöre yazılamıyor: %w", err)
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return nil
}
