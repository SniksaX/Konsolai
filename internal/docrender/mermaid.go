package docrender

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Mermaid 11.17.2 (MIT), a self-contained browser bundle.
// Downloaded once from https://registry.npmjs.org/mermaid/-/mermaid-11.17.2.tgz
// path package/dist/mermaid.min.js. The MIT license is assets/LICENSE.

//go:embed assets/mermaid-11.17.2.min.js
var mermaidJS []byte

const mermaidBoot = `
mermaid.initialize({ startOnLoad: false, securityLevel: "strict" });
(async function () {
  var loadMs = Math.round(performance.now());
  var nodes = document.querySelectorAll("pre.mermaid");
  var marker = document.createElement("div");
  marker.id = "mermaid-done";
  marker.setAttribute("data-load-ms", String(loadMs > 0 ? loadMs : 0));
  function finish(status, index, msg, runMs) {
    marker.setAttribute("data-status", status);
    marker.setAttribute("data-run-ms", String(runMs > 0 ? runMs : 0));
    if (index) marker.setAttribute("data-index", String(index));
    if (msg) marker.textContent = msg;
    document.body.appendChild(marker);
  }
  for (var i = 0; i < nodes.length; i++) {
    try {
      await mermaid.parse(nodes[i].textContent);
    } catch (e) {
      var msg = (e && (e.str || e.message)) || String(e);
      finish("error", i + 1, msg, 0);
      return;
    }
  }
  var runStart = performance.now();
  try {
    await mermaid.run({ querySelector: "pre.mermaid" });
    finish("ok", 0, "", Math.round(performance.now() - runStart));
  } catch (e) {
    finish("error", 1, (e && (e.str || e.message)) || String(e), Math.round(performance.now() - runStart));
  }
})();
`

const mermaidAttempts = 3

func applyMermaid(ctx context.Context, page string) (string, error) {
	bin, err := FindChrome()
	if err != nil {
		return "", fmt.Errorf("docrender: %w", err)
	}
	dir, err := os.MkdirTemp("", "konsolai-mmd-")
	if err != nil {
		return "", fmt.Errorf("docrender: mermaid temp: %w", err)
	}
	defer os.RemoveAll(dir)
	htmlPath := filepath.Join(dir, "doc.html")
	body := markMermaidBlocks(page)
	body = insertMermaidScript(body)
	if err := os.WriteFile(filepath.Join(dir, "mermaid.min.js"), mermaidJS, 0644); err != nil {
		return "", fmt.Errorf("docrender: mermaid write: %w", err)
	}
	if err := os.WriteFile(htmlPath, []byte(body), 0600); err != nil {
		return "", fmt.Errorf("docrender: mermaid write: %w", err)
	}
	dom, err := waitMermaidDOM(ctx, bin, dir, htmlPath)
	if err != nil {
		return "", err
	}
	status, index, message, ok := mermaidMarker(dom)
	if !ok {
		return "", fmt.Errorf("docrender: mermaid: rendering did not finish")
	}
	if status == "error" {
		if index < 1 {
			index = 1
		}
		if message == "" {
			message = "render failed"
		}
		return "", fmt.Errorf("docrender: mermaid diagram %d: %s", index, message)
	}
	return cleanMermaidDOM(dom), nil
}

// mermaid.run reads the node's innerHTML. A nested code element would be
// parsed as the diagram text, so the fence body is the pre's only content.
var reMermaidFence = regexp.MustCompile(`(?is)<pre><code class="[^"]*\blanguage-mermaid\b[^"]*">(.*?)</code></pre>`)

func markMermaidBlocks(page string) string {
	return reMermaidFence.ReplaceAllString(page, `<pre class="mermaid">$1</pre>`)
}

func insertMermaidScript(page string) string {
	script := "<script src=\"mermaid.min.js\"></script>\n<script>\n" + mermaidBoot + "\n</script>\n"
	if i := strings.LastIndex(page, "</body>"); i >= 0 {
		return page[:i] + script + page[i:]
	}
	return page + script
}

func waitMermaidDOM(ctx context.Context, bin, dir, htmlPath string) (string, error) {
	var last string
	for attempt := 0; attempt < mermaidAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		dom, err := dumpDOM(ctx, bin, dir, htmlPath, attempt)
		if err != nil {
			return "", err
		}
		last = dom
		if _, _, _, ok := mermaidMarker(dom); ok {
			return dom, nil
		}
	}
	if strings.TrimSpace(last) == "" {
		return "", fmt.Errorf("docrender: mermaid: rendering did not finish")
	}
	return "", fmt.Errorf("docrender: mermaid: rendering did not finish")
}

func dumpDOM(ctx context.Context, bin, dir, htmlPath string, attempt int) (string, error) {
	profile := filepath.Join(dir, fmt.Sprintf("user-data-%d", attempt))
	if err := os.Mkdir(profile, 0700); err != nil {
		return "", fmt.Errorf("docrender: mermaid profile: %w", err)
	}
	pageURL, err := fileURL(htmlPath)
	if err != nil {
		return "", fmt.Errorf("docrender: mermaid path: %w", err)
	}
	wait := scaledRenderTimeout()
	ms := int(wait / time.Millisecond)
	if ms < 1000 {
		ms = 1000
	}
	chromeMS := ms - 2000
	if chromeMS < 5000 {
		chromeMS = ms
	}
	args := append(headlessArgs(profile),
		fmt.Sprintf("--virtual-time-budget=%d", ms),
		fmt.Sprintf("--timeout=%d", chromeMS),
		"--run-all-compositor-stages-before-draw",
		"--dump-dom",
		pageURL,
	)
	t0 := time.Now()
	stdout, stderr, launch, err := runGroupedClock(ctx, wait, dir, chromeEnv(), true, bin, args...)
	noteRenderTrace(launch, time.Since(t0), string(stdout))
	if err != nil {
		return "", fmt.Errorf("docrender: chrome: %s", chromeFail(stderr, err, wait, "diagram render"))
	}
	return string(stdout), nil
}

var (
	reDoneDiv  = regexp.MustCompile(`(?is)<div\b([^>]*\bid="mermaid-done"[^>]*)>(.*?)</div>`)
	reDataAttr = regexp.MustCompile(`(?i)\bdata-(status|index)="([^"]*)"`)
	reScript   = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	reTooltip  = regexp.MustCompile(`(?is)<div\b[^>]*\bclass="mermaidTooltip"[^>]*>.*?</div>`)
	reSVGEl    = regexp.MustCompile(`(?is)<svg\b[^>]*>.*?</svg>`)
)

func mermaidMarker(dom string) (status string, index int, message string, ok bool) {
	m := reDoneDiv.FindStringSubmatch(dom)
	if m == nil {
		return "", 0, "", false
	}
	for _, am := range reDataAttr.FindAllStringSubmatch(m[1], -1) {
		switch strings.ToLower(am[1]) {
		case "status":
			status = am[2]
		case "index":
			index, _ = strconv.Atoi(am[2])
		}
	}
	message = strings.TrimSpace(htmlUnescape(m[2]))
	return status, index, message, true
}

func cleanMermaidDOM(dom string) string {
	dom = reDoneDiv.ReplaceAllString(dom, "")
	dom = reTooltip.ReplaceAllString(dom, "")
	dom = reScript.ReplaceAllString(dom, "")
	dom = reSVGEl.ReplaceAllStringFunc(dom, func(s string) string {
		return inlineSVG([]byte(s))
	})
	return dom
}

func htmlUnescape(s string) string {
	r := strings.NewReplacer(
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#34;", `"`,
		"&#39;", "'",
		"&amp;", "&",
	)
	return r.Replace(s)
}
