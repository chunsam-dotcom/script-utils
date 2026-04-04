package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const indexHTML = `
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Java Class Relationship Map</title>
    <script type="text/javascript" src="https://unpkg.com/vis-network/standalone/umd/vis-network.min.js"></script>
    <style>
        body { margin: 0; background: #f4f7f9; font-family: sans-serif; }
        #network { width: 100%; height: 100vh; }
        .panel { position: absolute; top: 20px; left: 20px; background: white; padding: 20px; 
                 border-radius: 12px; box-shadow: 0 4px 20px rgba(0,0,0,0.1); z-index: 10; width: 280px; }
        h2 { margin: 0 0 10px 0; font-size: 18px; color: #2c3e50; }
        .stat { font-size: 13px; color: #7f8c8d; line-height: 1.6; }
    </style>
</head>
<body>
    <div class="panel">
        <h2>Class Dependency Map</h2>
        <div id="status" class="stat">분석 중...</div>
        <div class="stat">• 휠로 확대/축소, 드래그로 이동 가능</div>
    </div>
    <div id="network"></div>
    <script>
        fetch('/data').then(res => res.json()).then(data => {
            document.getElementById('status').innerText = "총 " + data.nodes.length + "개의 클래스 분석 완료";
            const container = document.getElementById('network');
            
            const options = {
                nodes: {
                    shape: 'box',
                    margin: 10,
                    font: { face: 'monospace', size: 14 },
                    color: { background: '#ffffff', border: '#34495e' }
                },
                edges: {
                    arrows: 'to',
                    color: '#bdc3c7',
                    smooth: { type: 'cubicBezier' }
                },
                // 물리 엔진 설정 변경: 초기 배치 후 움직이지 않게 설정
                physics: {
                    enabled: true,
                    solver: 'forceAtlas2Based',
                    forceAtlas2Based: { 
                        gravitationalConstant: -150, 
                        centralGravity: 0.01, 
                        springLength: 100 
                    },
                    stabilization: {
                        enabled: true,
                        iterations: 1000, // 충분히 미리 계산해서
                        updateInterval: 25
                    }
                }
            };
            const network = new vis.Network(container, data, options);
            
            // 초기 배치가 끝나면 물리 엔진을 꺼서 멈추게 함
            network.once("stabilizationIterationsDone", function () {
                network.setOptions({ physics: { enabled: false } });
                document.getElementById('status').innerText += " (배치 고정됨)";
            });
        });
    </script>
</body>
</html>
`

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type GraphData struct {
	Nodes []map[string]string `json:"nodes"`
	Edges []Edge              `json:"edges"`
}

var (
	excludeMap = make(map[string]bool) // 필터링용 맵 추가
	nodeMap    = make(map[string]bool)
	edgeMap    = make(map[string]bool)
	data       = GraphData{Nodes: []map[string]string{}, Edges: []Edge{}}
	mu         sync.Mutex
)

// exclude.txt 로드 함수 추가
func loadExcludeList() {
	excludeMap = make(map[string]bool)
	file, err := os.Open("exclude.txt")
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			excludeMap[line] = true
		}
	}
}

func analyzeClass(classPath string, className string) {
	// 시작 클래스가 제외 대상이면 중단
	if excludeMap[className] {
		return
	}

	cmd := exec.Command("javap", "-v", classPath)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return
	}

	re := regexp.MustCompile(`Class\s+.*\s+//\s+([\w/$]+)`)
	matches := re.FindAllStringSubmatch(out.String(), -1)

	mu.Lock()
	defer mu.Unlock()

	for _, m := range matches {
		targetFull := strings.ReplaceAll(m[1], "/", ".")
		parts := strings.Split(targetFull, ".")
		targetClass := parts[len(parts)-1]

		// 내부 클래스, 자바 표준, 그리고 excludeMap에 있는 클래스 제외
		if strings.Contains(targetFull, "$") || strings.HasPrefix(targetFull, "java.") ||
			strings.HasPrefix(targetFull, "sun.") || excludeMap[targetClass] || excludeMap[targetFull] {
			continue
		}

		if targetClass != "" && targetClass != className {
			if !nodeMap[targetClass] {
				data.Nodes = append(data.Nodes, map[string]string{"id": targetClass, "label": targetClass})
				nodeMap[targetClass] = true
			}
			edgeKey := className + "->" + targetClass
			if !edgeMap[edgeKey] {
				data.Edges = append(data.Edges, Edge{From: className, To: targetClass})
				edgeMap[edgeKey] = true
			}
		}
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("\nYou can specify a directory containing .class files to analyze.")
		fmt.Println("exclude.txt file is used to filter out classes from the analysis.")
		fmt.Println("\nUsage: go run java_analyzer.go <java_bin_directory>\n")
		return
	}
	targetDir, _ := filepath.Abs(os.Args[1])

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, indexHTML)
	})

	http.HandleFunc("/data", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		loadExcludeList() // 분석 시작 전 필터 리스트 로드
		nodeMap = make(map[string]bool)
		edgeMap = make(map[string]bool)
		data = GraphData{Nodes: []map[string]string{}, Edges: []Edge{}}
		mu.Unlock()

		var wg sync.WaitGroup
		filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && filepath.Ext(path) == ".class" && !strings.Contains(info.Name(), "$") {
				className := strings.TrimSuffix(info.Name(), ".class")

				// 제외 대상이면 노드 생성 건너뜀
				if excludeMap[className] {
					return nil
				}

				fmt.Printf("✔ 분석 중: %s\n", info.Name())
				mu.Lock()
				if !nodeMap[className] {
					data.Nodes = append(data.Nodes, map[string]string{"id": className, "label": className})
					nodeMap[className] = true
				}
				mu.Unlock()

				wg.Add(1)
				go func(p, c string) {
					defer wg.Done()
					analyzeClass(p, c)
				}(path, className)
			}
			return nil
		})
		wg.Wait()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	})

	fmt.Printf("🚀 서버 실행: http://localhost:8080\n📂 분석 대상: %s\n", targetDir)
	http.ListenAndServe(":8080", nil)
}
