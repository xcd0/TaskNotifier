"""既存の圧縮Web UIへlimited-tasks.jsを反映し、ビルド用ハッシュを更新する。"""
import base64
import gzip
import hashlib
from pathlib import Path
import re


def main():
	root = Path(__file__).resolve().parent.parent
	parts = sorted((root / "ci-generated").glob("webview.part*"))
	if len(parts) != 3:
		raise ValueError("Expected exactly three WebView chunks")
	html = gzip.decompress(base64.b64decode("".join(p.read_text().strip() for p in parts))).decode()
	helper = (root / "web/limited-tasks.js").read_text(encoding="utf-8")
	begin, end = "/* LIMITED_TASKS_BEGIN */", "/* LIMITED_TASKS_END */"
	if html.count(begin) != 1 or html.count(end) != 1:
		raise ValueError("Limited task source markers are missing or duplicated")
	html = html[:html.index(begin)] + begin + "\n" + helper + "\n" + html[html.index(end):]
	encoded = base64.b64encode(gzip.compress(html.encode(), mtime=0)).decode()
	size = (len(encoded) + 2) // 3
	for index, part in enumerate(parts):
		part.write_text(encoded[index * size:(index + 1) * size] + "\n", encoding="ascii")
	pwa = html.replace('<title>TaskNotifier</title>\n\t', '<title>TaskNotifier</title>\n\t<link rel="manifest" href="./manifest.webmanifest"><meta name="theme-color" content="#ffffff"><link rel="icon" href="./icons/app-192.png">')
	build = root / "scripts/build.ps1"
	text = build.read_text(encoding="utf-8")
	for name, content in [("webViewHash", html), ("pwaHash", pwa)]:
		digest = hashlib.sha256(content.encode()).hexdigest()
		text, count = re.subn(r'(\$' + name + r' -ne ")[a-f0-9]{64}("\))', lambda m: m[1] + digest + m[2], text)
		if count != 1:
			raise ValueError(f"Missing hash check: {name}")
	build.write_text(text, encoding="utf-8")


if __name__ == "__main__":
	main()
