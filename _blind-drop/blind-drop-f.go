package main

import (
	"flag"
	"fmt"
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
	uploadDir := flag.String("dir", "", "수신용 임시 디렉토리 (필수)")
	moveDir := flag.String("move", "", "최종 보관 디렉토리 (선택, 덮어쓰기 허용)")
	token := flag.String("token", "", "Bearer 토큰 인증 (선택)")
	port := flag.String("port", "8085", "서버 포트")
	maxFileMB := flag.Int64("max-file", 500, "파일당 최대 크기 (MB)")
	maxTotalMB := flag.Int64("max-total", 10240, "전체 합산 용량 제한 (MB)")
	maxConn := flag.Int("max-conn", 5, "동시 접속 제한")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "\n[ blind-drop ] - Secure Data Blackhole (Overwrite Enabled)\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nClient Examples:\n")
		fmt.Fprintf(os.Stderr, "  curl -H \"Authorization: Bearer <TOKEN>\" -F \"file=@db.sql\" http://localhost:%s/drop\n", *port)
		fmt.Fprintf(os.Stderr, "  curl -H \"Authorization: Bearer <TOKEN>\" -F \"file=@file.zip\" -F \"path=dir/file.zip\" http://localhost:%s/drop\n\n", *port)
	}
	flag.Parse()

	if *uploadDir == "" {
		flag.Usage()
		os.Exit(1)
	}

	basePath, _ := filepath.Abs(*uploadDir)
	os.MkdirAll(basePath, 0700)
	var finalTargetDir string
	if *moveDir != "" {
		finalTargetDir, _ = filepath.Abs(*moveDir)
		os.MkdirAll(finalTargetDir, 0700)
	}

	limitFile := *maxFileMB * MB
	limitTotal := *maxTotalMB * MB
	sem := make(chan struct{}, *maxConn)

	http.HandleFunc("/drop", func(w http.ResponseWriter, r *http.Request) {
		if *token != "" && r.Header.Get("Authorization") != "Bearer "+*token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}

		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		default:
			http.Error(w, "Server Busy", http.StatusTooManyRequests)
			return
		}

		// 1. 용량 체크 (현재 합산량 확인)
		if getDirsSize(basePath, finalTargetDir) >= limitTotal {
			http.Error(w, "Storage Quota Exceeded", http.StatusInsufficientStorage)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, limitFile)
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "File too large or invalid", http.StatusRequestEntityTooLarge)
			return
		}
		defer file.Close()

		clientPath := r.FormValue("path")
		cleanRel := filepath.Clean(strings.TrimPrefix(clientPath, "/"))
		if strings.HasPrefix(cleanRel, "..") {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		tempPath := filepath.Join(basePath, cleanRel)
		if clientPath == "" || strings.HasSuffix(clientPath, "/") {
			tempPath = filepath.Join(basePath, cleanRel, header.Filename)
		}

		finalPath := tempPath
		if finalTargetDir != "" {
			finalPath = filepath.Join(finalTargetDir, cleanRel)
			if clientPath == "" || strings.HasSuffix(clientPath, "/") {
				finalPath = filepath.Join(finalTargetDir, cleanRel, header.Filename)
			}
		}

		// 2. 중복 체크 로직 수정:
		// - move 설정이 없으면 기존처럼 Conflict 에러 (안전장치)
		// - move 설정이 있으면 덮어쓰기 허용 (로그만 남김)
		if _, err := os.Stat(finalPath); err == nil {
			if finalTargetDir == "" {
				http.Error(w, "Conflict: Already exists", http.StatusConflict)
				return
			}
			log.Printf("Notice: File %s already exists. Will be overwritten.", finalPath)
		}

		// 3. 파일 저장
		os.MkdirAll(filepath.Dir(tempPath), 0700)
		dst, err := os.Create(tempPath)
		if err != nil {
			http.Error(w, "Internal Error", 500)
			return
		}

		if _, err := io.Copy(dst, file); err != nil {
			dst.Close()
			return
		}
		dst.Close()

		// 4. 이동 (이때 기존 파일이 있으면 덮어씌워짐)
		if finalTargetDir != "" {
			os.MkdirAll(filepath.Dir(finalPath), 0700)
			// os.Rename은 대상이 존재할 경우 덮어씁니다 (Unix 기준)
			if err := os.Rename(tempPath, finalPath); err != nil {
				log.Printf("Move Error: %v", err)
			} else {
				log.Printf("[%s] Received & Updated: %s", time.Now().Format("15:04:05"), finalPath)
			}
		} else {
			log.Printf("[%s] Received: %s", time.Now().Format("15:04:05"), tempPath)
		}

		w.WriteHeader(http.StatusCreated)
	})

	log.Printf("--- blind-drop (v1.3.0) ---")
	log.Printf("Mode: Overwrite enabled on Move path")
	log.Printf("Listen: :%s, MaxFile: %dMB, MaxTotal: %dMB", *port, *maxFileMB, *maxTotalMB)
	log.Fatal(http.ListenAndServe(":"+*port, nil))
}
