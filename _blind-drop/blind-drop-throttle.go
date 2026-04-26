package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const MB = 1024 * 1024

var globalUsedBytes int64 // 5분마다 갱신되는 유일한 장부

// [핵심] 전송과 독립적으로 5분마다 디스크를 훑는 스캐너
func startCapacityChecker(root string) {
	for {
		var size int64
		err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				size += info.Size()
			}
			return nil
		})
		if err != nil {
			log.Printf("[Scanner Warning] Walk error: %v", err)
		}
		atomic.StoreInt64(&globalUsedBytes, size)
		log.Printf("[Scanner] Current Storage: %.2f MB", float64(size)/float64(MB))

		time.Sleep(5 * time.Minute)
	}
}

func throttledCopy(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	if limit <= 0 {
		return io.Copy(dst, src)
	}
	buf := make([]byte, 32*1024)
	var total int64
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				total += int64(nw)
				time.Sleep(time.Duration(int64(nw)) * time.Second / time.Duration(limit))
			}
			// 쓰기 에러 또는 짧은 쓰기 체크
			if ew != nil {
				return total, ew
			}
			if nr != nw {
				return total, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return total, nil
			}
			return total, er
		}
	}
}

func main() {
	port := flag.String("port", "9000", "Port")
	maxMB := flag.Int64("max", 10240, "Limit MB")
	auth := flag.String("auth", "", "user:pass (e.g. admin:1234)")
	flag.Parse()

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Critical: Cannot get home dir: %v", err)
	}

	root := filepath.Join(home, ".blind-drop")
	inDir := filepath.Join(root, "incoming")
	outDir := filepath.Join(root, "moved")

	// [복구] 초기 폴더 생성 및 에러 체크 (필수)
	if err := os.MkdirAll(inDir, 0700); err != nil {
		log.Fatalf("Critical: Failed to create incoming: %v", err)
	}
	if err := os.MkdirAll(outDir, 0700); err != nil {
		log.Fatalf("Critical: Failed to create moved: %v", err)
	}

	// 백그라운드 스캐너 시작
	go startCapacityChecker(root)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 1. 인증 체크
		if *auth != "" {
			u, p, ok := r.BasicAuth()
			if !ok || u+":"+p != *auth {
				w.Header().Set("WWW-Authenticate", `Basic realm="restricted"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		// [복구] HTTP Method 체크 (스크린샷 로직)
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		if r.ContentLength > 3*1024*MB {
			http.Error(w, "File Too Large (Max 3GB)", 413)
			return
		}

		// [복구] 경로 유효성 및 보안 체크 (스크린샷 로직)
		cleanRel := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if cleanRel == "." || strings.HasPrefix(cleanRel, "..") {
			http.Error(w, "Invalid Path", http.StatusBadRequest)
			return
		}

		tmpPath := filepath.Join(inDir, cleanRel)
		finalPath := filepath.Join(outDir, cleanRel)

		// [복구] 중복 체크
		if _, err := os.Stat(finalPath); err == nil {
			http.Error(w, "Conflict", http.StatusConflict)
			return
		}

		// 2. 속도 결정 (백그라운드에서 계산된 값 활용)
		currentSize := atomic.LoadInt64(&globalUsedBytes)
		maxBytes := *maxMB * MB
		availPct := float64(maxBytes-currentSize) / float64(maxBytes) * 100

		var speed int64
		if availPct <= 0 {
			http.Error(w, "Storage Full", http.StatusInsufficientStorage)
			return
		}
		if availPct < 80 {
			speed = int64(float64(100*MB) * (availPct / 80.0))
			if speed < 100*1024 {
				speed = 100 * 1024
			}
		}

		// 3. 전송 시작 (All or Nothing)
		if err := os.MkdirAll(filepath.Dir(tmpPath), 0700); err != nil {
			log.Printf("[Error] Subdir create failed: %v", err)
			http.Error(w, "Internal Error", 500)
			return
		}

		dst, err := os.Create(tmpPath)
		if err != nil {
			log.Printf("[Error] File create failed: %v", err)
			http.Error(w, "Disk Error", 500)
			return
		}

		_, copyErr := throttledCopy(dst, r.Body, speed)
		syncErr := dst.Sync()
		closeErr := dst.Close()

		// 에러 무관용 원칙 적용
		if copyErr != nil || syncErr != nil || closeErr != nil {
			log.Printf("[Abort] Error during transfer: %s", cleanRel)
			_ = os.Remove(tmpPath)
			http.Error(w, "Transfer Incomplete", 500)
			return
		}

		// 4. 원자적 이동 및 최종 에러 체크
		if err := os.MkdirAll(filepath.Dir(finalPath), 0700); err != nil {
			_ = os.Remove(tmpPath)
			http.Error(w, "Internal Error", 500)
			return
		}

		if err := os.Rename(tmpPath, finalPath); err != nil {
			log.Printf("[Critical] Rename failed: %v", err)
			_ = os.Remove(tmpPath)
			http.Error(w, "Finalization Error", 500)
			return
		}

		w.WriteHeader(http.StatusCreated)
	})

	log.Printf("🚀 Server on :%s (Max: %dMB)", *port, *maxMB)
	log.Fatal(http.ListenAndServe(":"+*port, nil))
}
