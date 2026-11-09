package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func main() {
	platformInit()
	initLog()
	banner()

	code := 0
	func() {
		defer func() {
			if r := recover(); r != nil {
				if r == panicExit {
					code = 1
					return
				}
				// Beklenmeyen hata: kullaniciya goster ve loga yaz.
				fmt.Println()
				fmt.Printf("BEKLENMEYEN HATA: %v\n", r)
				fmt.Println()
				fmt.Println("Bu ekranin fotografini ve wr854-yedek-log.txt dosyasini")
				fmt.Println("bize gonderin.")
				code = 2
			}
		}()
		code = run()
	}()

	pauseBeforeExit()
	flushLog()
	os.Exit(code)
}

// panicExit, fatal()'in akisi durdurmak icin kullandigi ic sinyal.
// os.Exit kullanmadigimiz icin ekran ve log dosyasi son sozunu soyleyebiliyor.
var panicExit = struct{ s string }{"fatal"}

// run, programin asil akisi.
func run() int {
	// ---- 1) credentials ------------------------------------------------
	ip := ask("Cihaz IP adresi", defaultIP)
	user := ask("Web arayüzü kullanıcı adı", "admin")
	pass, err := askPassword("Web arayüzü şifresi")
	if err != nil {
		fatal("şifre okunamadı: %v", err)
	}

	// ---- 2) destination ------------------------------------------------
	defDir := defaultBackupDir(ip)
	outDir := ask("Yedeğin kaydedileceği klasör", defDir)
	outDir = expandPath(outDir)
	if err := ensureDir(outDir); err != nil {
		fatal("%v", err)
	}
	abs, _ := filepath.Abs(outDir)
	fmt.Printf("  -> %s\n", abs)

	// ---- 3) preflight --------------------------------------------------
	fmt.Println()
	fmt.Println("[3/5] Bağlantılar kontrol ediliyor...")

	dev := newDevice(ip, user, pass)

	if err := dev.reachable(); err != nil {
		fatal("cihaza ulaşılamıyor (%s): %v\n"+
			"     Cihaz açık mı, aynı ağda mısınız, IP doğru mu?", ip, err)
	}
	ok("cihaz yanıt veriyor  (%s)", ip)

	if err := dev.login(); err != nil {
		fatal("giriş başarısız: %v", err)
	}
	ok("web arayüzü girişi kabul edildi")

	if err := dev.verifySession(); err != nil {
		fatal("%v", err)
	}
	ok("yükleme sayfası erişilebilir")

	host, err := localIPFor(ip)
	if err != nil {
		fatal("bu makinenin cihaza bakan ağ adresi bulunamadı: %v", err)
	}
	ok("bu makinenin adresi  %s", host)

	for _, p := range partitions {
		port := portBase + p.index
		if !portFree(port) {
			fatal("yerel port %d kullanımda (mtd%d_%s için gerekli).\n"+
				"     Başka bir yedek aracı veya dinleyici açık olabilir.", port, p.index, p.name)
		}
	}
	ok("yerel portlar hazır  (%d-%d)", portBase, portBase+len(partitions)-1)

	// Windows güvenlik duvarı ilk çalıştırmada sorabilir; önceden uyar.
	fmt.Println()
	fmt.Println("  NOT: Windows güvenlik duvarı bu program için izin sorarsa")
	fmt.Println("       \"Özel ağlar\" kutusunu işaretleyip İzin Ver'i seçin.")
	fmt.Println("       Aksi halde cihaz bağlanamaz ve yedek boş kalır.")

	if !confirm("Yedek alınsın mı") {
		fmt.Println("İptal edildi.")
		return 0
	}

	// ---- 4) send payload ----------------------------------------------
	fmt.Println()
	fmt.Println("[4/5] Cihaza yedek payload'ı gönderiliyor...")

	script := payloadScript(host)
	tarBytes, err := buildTar(script)
	if err != nil {
		fatal("payload paketlenemedi: %v", err)
	}
	ok("payload hazır  (%d bayt)", len(tarBytes))

	// Start listeners BEFORE the upload so nothing is missed.
	type progressState struct {
		mu sync.Mutex
		m  map[int]int64
	}
	st := &progressState{m: map[int]int64{}}

	rxDone := make(chan []rxResult, 1)
	go func() {
		res, _ := receiveAll(abs, host, 30*time.Minute, func(p partition, n int64) {
			st.mu.Lock()
			prev := st.m[p.index]
			st.m[p.index] = n
			st.mu.Unlock()
			// report roughly every 16 MiB so the console stays readable
			if prev/(16<<20) != n/(16<<20) {
				reportProgress(p, n)
			}
		})
		rxDone <- res
	}()

	// Give the listeners a moment to bind.
	time.Sleep(700 * time.Millisecond)

	msg, err := dev.upload(tarBytes)
	if err != nil {
		fatal("payload gönderilemedi: %v", err)
	}
	ok("cihaz payload'ı aldı ve çalıştırdı")
	if msg != "" {
		fmt.Printf("  cihaz yanıtı: %s\n", msg)
	}

	// ---- 5) collect ----------------------------------------------------
	fmt.Println()
	fmt.Println("[5/5] Döküm alınıyor...")

	// Wait for the streams, but do not hang forever if the device stalls.
	var results []rxResult
	select {
	case results = <-rxDone:
	case <-time.After(30 * time.Minute):
		fatal("zaman aşımı: cihazdan veri akışı tamamlanmadı")
	}

	fmt.Println()
	fmt.Println("=== SONUÇ ===")

	var totalBytes int64
	failed := 0
	for _, r := range results {
		if r.err != nil {
			fmt.Printf("  HATA  %-24s %v\n", r.p.fileName(), r.err)
			failed++
			continue
		}
		totalBytes += r.n
		note := ""
		if r.n == 0 {
			note = "  <-- BOŞ (cihaz bağlanamadı)"
			failed++
		}
		fmt.Printf("  %-24s %14s%s\n", r.p.fileName(), humanBytes(r.n), note)
	}

	fmt.Println()
	if failed > 0 {
		fmt.Println("Bazı bölümler alınamadı. Muhtemel nedenler:")
		fmt.Println("  - Windows güvenlik duvarı programı engelledi")
		fmt.Println("  - Cihaz bu makineye ulaşamıyor (farklı VLAN/alt ağ)")
		fmt.Println("  - Port 9101-9105 aralığı başka bir program tarafından kullanılıyor")
		return 1
	}

	if err := writeChecksums(abs, results); err != nil {
		fmt.Printf("  (md5sums.txt yazılamadı: %v)\n", err)
	}
	fmt.Printf("Toplam: %s\n", humanBytes(totalBytes))
	fmt.Printf("Klasör: %s\n", abs)
	fmt.Println()
	fmt.Println("Yedek tamamlandı. Cihazın flash'ına hiçbir şey yazılmadı,")
	fmt.Println("boot environment değişmedi ve cihaz yeniden başlatılmadı.")
	return 0
}

// ---------------------------------------------------------------- output

func banner() {
	fmt.Println("======================================================")
	fmt.Printf("  %s  v%s\n", appName, appVersion)
	fmt.Println("  Aidata WR854GVR (RTL9607F) stok firmware")
	fmt.Println("======================================================")
	fmt.Println("  Bu araç cihazın flash'ına YAZMAZ. Salt-okunur")
	fmt.Println("  nanddump ile yedek alır; boot env değişmez,")
	fmt.Println("  cihaz yeniden başlatılmaz.")
	fmt.Println("======================================================")
	fmt.Println()
}

func ok(format string, a ...any) {
	fmt.Printf("  [OK] "+format+"\n", a...)
}

func fatal(format string, a ...any) {
	fmt.Println()
	fmt.Printf("HATA: "+format+"\n", a...)
	panic(panicExit)
}

// pauseBeforeExit, cift tiklayarak calistirilan bir .exe'de pencerenin
// kapanmasini engeller. Boylece kullanici hata mesajini okuyabilir ve
// bize iletebilir. Konsol yoksa (yonlendirilmis cikti) sessizce gecer.
func pauseBeforeExit() {
	fmt.Println()
	if p := logPath(); p != "" {
		fmt.Printf("Log dosyası: %s\n", p)
	}
	// Sadece gercek bir konsol varsa bekle; cikti bir dosyaya/boruya
	// yonlendirilmisse beklemeden gec.
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return
	}
	fmt.Println("Kapatmak icin Enter'a basin...")
	stdin.ReadString('\n')
}

func reportProgress(p partition, n int64) {
	pct := float64(0)
	if p.size > 0 {
		pct = float64(n) / float64(p.size) * 100
		if pct > 100 {
			pct = 100
		}
	}
	fmt.Printf("  %-24s %6.1f%%  %s\n", p.fileName(), pct, humanBytes(n))
}

func defaultBackupDir(ip string) string {
	base := os.Getenv("USERPROFILE")
	if base == "" {
		base = os.Getenv("HOME")
	}
	if base == "" {
		base = "."
	}
	stamp := time.Now().Format("20060102-1504")
	return filepath.Join(base, "Desktop", "WR854GVR-yedek-"+stamp)
}

func expandPath(p string) string {
	if strings.HasPrefix(p, "~") {
		home := os.Getenv("USERPROFILE")
		if home == "" {
			home = os.Getenv("HOME")
		}
		if home != "" {
			p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), string(os.PathSeparator)))
		}
	}
	// Accept Windows-style quoted paths pasted with quotes.
	p = strings.Trim(p, "\"'")
	return p
}

func writeChecksums(dir string, results []rxResult) error {
	var b strings.Builder
	for _, r := range results {
		sum, err := fileMD5(r.path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s  %s\n", sum, r.p.fileName())
	}
	return os.WriteFile(filepath.Join(dir, "md5sums.txt"), []byte(b.String()), 0644)
}
