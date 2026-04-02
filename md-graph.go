package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// vis.js 데이터 구조
type Node struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Group string `json:"group"`
	Path  string `json:"path,omitempty"` // 파일 노드일 경우 실제 경로
}

type Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

type GraphData struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// 내장 HTML 템플릿
const htmlTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Markdown Knowledge Graph</title>
    <script type="text/javascript" src="https://unpkg.com/vis-network/standalone/umd/vis-network.min.js"></script>
    <style>
        body { margin: 0; background: #1a1a1a; color: white; font-family: sans-serif; overflow: hidden; }
        #network-graph { width: 100vw; height: 100vh; }
        .controls { position: absolute; top: 20px; left: 20px; z-index: 10; background: rgba(40, 44, 52, 0.9); padding: 15px; border-radius: 8px; border: 1px solid #444; box-shadow: 0 4px 15px rgba(0,0,0,0.5); }
        h3 { margin: 0; color: #61afef; font-size: 16px; }
        .stat { font-size: 11px; color: #abb2bf; margin-top: 5px; }
        .hint { font-size: 10px; color: #5c6370; margin-top: 3px; }
    </style>
</head>
<body>
    <div class="controls">
        <h3>KNOWLEDGE GRAPH</h3>
        <div id="stat" class="stat">분석 중...</div>
        <div class="hint">노드 클릭: 로컬 편집기 열기 | 휠: 확대/축소</div>
    </div>
    <div id="network-graph"></div>
    <script>
        async function loadGraph() {
            try {
                const start = Date.now();
                const res = await fetch('/data');
                const graphData = await res.json();
                const elapsed = (Date.now() - start) / 1000;

                document.getElementById('stat').innerText = 
                    "Nodes: " + graphData.nodes.length + " | Edges: " + graphData.edges.length + " (" + elapsed.toFixed(2) + "s)";

                const container = document.getElementById('network-graph');
                const data = {
                    nodes: new vis.DataSet(graphData.nodes),
                    edges: new vis.DataSet(graphData.edges)
                };

                const options = {
                    nodes: { shape: 'dot', size: 16, font: { size: 12, color: '#ffffff' }, shadow: true },
                    edges: { width: 1.5, color: { inherit: 'from' }, arrows: 'to', smooth: { type: 'continuous' } },
                    groups: {
                        note: { color: { background: '#61afef', border: '#4b83b3' } },
                        tag: { color: { background: '#e06c75', border: '#be5046' }, shape: 'diamond' }
                    },
                    physics: {
                        barnesHut: { gravitationalConstant: -4000, springLength: 120, centralGravity: 0.2 },
                        stabilization: { iterations: 150 }
                    }
                };

                const network = new vis.Network(container, data, options);

                network.on("click", function (params) {
                    if (params.nodes.length > 0) {
                        const nodeId = params.nodes[0];
                        const node = data.nodes.get(nodeId);
                        if (node && node.path) {
                            fetch("/open?path=" + encodeURIComponent(node.path));
                        }
                    }
                });
            } catch (e) {
                document.getElementById('stat').innerText = "데이터 로드 실패";
            }
        }
        loadGraph();
    </script>
</body>
</html>`

// 마크다운 스캔 함수
func scanMarkdown(root string) GraphData {
	linkRegex := regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]+)?\]\]`)
	tagRegex := regexp.MustCompile(`(?:^|\s)#([a-zA-Z가-힣][\w가-힣]*)`)

	graph := GraphData{Nodes: []Node{}, Edges: []Edge{}}
	nodeCheck := make(map[string]bool)
	edgeCheck := make(map[string]bool)

	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}

		fileName := strings.TrimSuffix(filepath.Base(path), ".md")
		if !nodeCheck[fileName] {
			graph.Nodes = append(graph.Nodes, Node{ID: fileName, Label: fileName, Group: "note", Path: path})
			nodeCheck[fileName] = true
		}

		file, _ := os.Open(path)
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			// 링크 추출
			for _, m := range linkRegex.FindAllStringSubmatch(line, -1) {
				target := strings.TrimSpace(m[1])
				edgeID := fileName + "->" + target
				if !edgeCheck[edgeID] {
					graph.Edges = append(graph.Edges, Edge{From: fileName, To: target, Label: "links"})
					edgeCheck[edgeID] = true
				}
			}
			// 태그 추출
			for _, m := range tagRegex.FindAllStringSubmatch(line, -1) {
				tagName := "#" + strings.TrimSpace(m[1])
				if !nodeCheck[tagName] {
					graph.Nodes = append(graph.Nodes, Node{ID: tagName, Label: tagName, Group: "tag"})
					nodeCheck[tagName] = true
				}
				edgeID := fileName + "->" + tagName
				if !edgeCheck[edgeID] {
					graph.Edges = append(graph.Edges, Edge{From: fileName, To: tagName, Label: "tagged"})
					edgeCheck[edgeID] = true
				}
			}
		}
		return nil
	})
	return graph
}

// 시스템 기본 프로그램으로 열기
func openLocalFile(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	_ = cmd.Start()
}

func main() {
	targetPath := flag.String("path", ".", "스캔할 마크다운 폴더")
	port := flag.String("port", "8080", "서버 포트")
	flag.Parse()

	absRoot, _ := filepath.Abs(*targetPath)

	// 1. 메인 페이지
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, htmlTemplate)
	})

	// 2. 실시간 데이터 API (F5 새로고침 시마다 스캔)
	http.HandleFunc("/data", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		data := scanMarkdown(absRoot)
		fmt.Printf("🔄 [Scan] %d files, Took: %v\n", len(data.Nodes), time.Since(start))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	})

	// 3. 파일 오픈 API
	http.HandleFunc("/open", func(w http.ResponseWriter, r *http.Request) {
		fPath := r.URL.Query().Get("path")
		if fPath != "" {
			fmt.Printf("📂 [Open] %s\n", fPath)
			openLocalFile(fPath)
		}
	})

	fmt.Printf("🚀 Knowledge Graph Server 가동: http://localhost:%s\n", *port)
	fmt.Printf("📂 스캔 대상 경로: %s\n", absRoot)
	_ = http.ListenAndServe(":"+*port, nil)
}
