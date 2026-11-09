package main

// Cikti gunlugu.
//
// Kullanici programi cift tiklayarak calistiriyor; pencere kapaninca ya da
// fotograf cekmeyi unutunca elimizde hicbir sey kalmiyordu. Bu dosya
// ekrana yazilan HER SEYI exe'nin yanindaki wr854-yedek-log.txt dosyasina
// da yazar. Boylece "ne cikti verdi" sorusu bir dosyayla cevaplanabiliyor.
//
// Sifre ASLA loga yazilmaz (readPasswordNoEcho dogrudan konsoldan okur).
//
// Yontem: os.Stdout bir os.Pipe'in yazma ucuna cevrilir; bir goroutine
// okuma ucundan geleni hem gercek konsola hem log dosyasina kopyalar.
// Boylece mevcut tum fmt.Print* cagrilari degismeden loglanir.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const logFileName = "wr854-yedek-log.txt"

var (
	logMu      sync.Mutex
	logFile    *os.File
	logName    string
	pipeR      *os.File
	pipeW      *os.File
	pipeDone   chan struct{}
	realStdout *os.File
	savedErr   *os.File
)

// initLog, exe'nin bulundugu klasorde log dosyasini acar ve ciktiyi aynalar.
// Log acilamazsa program sessizce normal calismaya devam eder.
func initLog() {
	dir := exeDir()
	p := ""
	if dir != "" {
		p = filepath.Join(dir, logFileName)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil || p == "" {
		// Yazilamayan bir yerden calistirilmis olabilir: gecici klasore dus.
		p = filepath.Join(os.TempDir(), logFileName)
		f, err = os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return
		}
	}

	logMu.Lock()
	logFile, logName = f, p
	logMu.Unlock()

	fmt.Fprintf(f, "%s v%s - %s\n", appName, appVersion,
		time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(f, "%s\n\n", "======================================================")

	// Cikti aynalama: stdout -> boru -> (konsol + log).
	r, w, err := os.Pipe()
	if err != nil {
		return
	}
	pipeR, pipeW = r, w
	pipeDone = make(chan struct{})
	realStdout = os.Stdout
	savedErr = os.Stderr
	os.Stdout = w
	os.Stderr = w

	go func() {
		defer close(pipeDone)
		buf := make([]byte, 32<<10)
		dst := io.MultiWriter(realStdout, recWriter{})
		for {
			n, err := r.Read(buf)
			if n > 0 {
				dst.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
}

func exeDir() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(p)
}

func logPath() string {
	logMu.Lock()
	defer logMu.Unlock()
	return logName
}

// recWriter, log dosyasina yazan io.Writer (aynalama icin).
type recWriter struct{}

func (recWriter) Write(p []byte) (int, error) {
	logMu.Lock()
	defer logMu.Unlock()
	if logFile != nil {
		logFile.Write(p)
	}
	return len(p), nil
}

// flushLog, boruyu bosaltip log dosyasini kapar. Cikistan hemen once cagrilir.
func flushLog() {
	if pipeW != nil {
		os.Stdout = realStdout
		os.Stderr = savedErr
		pipeW.Close()
		<-pipeDone
		pipeR.Close()
		pipeW = nil
	}
	logMu.Lock()
	defer logMu.Unlock()
	if logFile != nil {
		logFile.Sync()
		logFile.Close()
		logFile = nil
	}
}
