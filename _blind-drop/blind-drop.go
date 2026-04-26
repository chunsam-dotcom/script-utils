package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const MB = 1024 * 1024

func getDirsSize(paths ...string) int64 {
	var total int64
	for _, path := range paths {
		if path == "" {
			continue
		}
		filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				total += info.Size()
			}
			return nil
		})
	}
	return total
}

func main() {
	uploadDir := flag.String("dir", "", "Base directory for receiving (Required)")
	moveDir := flag.String("move", "", "Final destination directory (Optional)")
	userPass := flag.String("auth", "", "Basic Auth (user:pass)")
	port := flag.String("port", "8082", "Server port")
	maxTotalMB := flag.Int64("max-total", 10240, "Max total storage limit in MB")

	flag.Parse()

	if *uploadDir == "" {
		flag.Usage()
		os.Exit(1)
	}

	basePath, _ := filepath.Abs(*uploadDir)
	var finalTargetDir string
	if *moveDir != "" {
		finalTargetDir, _ = filepath.Abs(*moveDir)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 1. Basic Auth 인증
		if *userPass != "" {
			u, p, ok := r.BasicAuth()
			expected := strings.SplitN(*userPass, ":", 2)
			if !ok || u != expected[0] || p != expected[1] {
				w.Header().Set("WWW-Authenticate", `Basic realm="restricted"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		// 2. PUT 또는 POST 허용
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		// 3. URL에서 경로 추출 (폴더 구조 유지의 핵심)
		cleanRel := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if cleanRel == "." || strings.HasPrefix(cleanRel, "..") {
			http.Error(w, "Invalid Path", http.StatusBadRequest)
			return
		}

		// 용량 체크
		if getDirsSize(basePath, finalTargetDir) >= *maxTotalMB*MB {
			http.Error(w, "Storage Full", http.StatusInsufficientStorage)
			return
		}

		tempPath := filepath.Join(basePath, cleanRel)
		finalPath := tempPath
		if finalTargetDir != "" {
			finalPath = filepath.Join(finalTargetDir, cleanRel)
		}

		// 중복 체크 (move 모드 아닐 때만)
		if finalTargetDir == "" {
			if _, err := os.Stat(finalPath); err == nil {
				http.Error(w, "Conflict", http.StatusConflict)
				return
			}
		}

		// 4. 파일 저장
		os.MkdirAll(filepath.Dir(tempPath), 0700)
		dst, err := os.Create(tempPath)
		if err != nil {
			http.Error(w, "Internal Error", 500)
			return
		}

		// r.Body에서 직접 복사 (Multipart 아님)
		_, err = io.Copy(dst, r.Body)
		dst.Close()
		if err != nil {
			os.Remove(tempPath)
			return
		}

		// 5. 완료 후 이동
		if finalTargetDir != "" {
			os.MkdirAll(filepath.Dir(finalPath), 0700)
			os.Rename(tempPath, finalPath)
			log.Printf("[%s] Received & Moved: %s", time.Now().Format("15:04:05"), finalPath)
		} else {
			log.Printf("[%s] Received: %s", time.Now().Format("15:04:05"), tempPath)
		}

		w.WriteHeader(http.StatusCreated)
	})

	log.Printf("blind-drop started on :%s", *port)
	log.Fatal(http.ListenAndServe(":"+*port, nil))
}
