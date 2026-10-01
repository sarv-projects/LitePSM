import urllib.request
import re
import json

url = "https://raw.githubusercontent.com/punkpeye/awesome-mcp-servers/main/README.md"
req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
with urllib.request.urlopen(req) as resp:
    lines = resp.read().decode("utf-8", errors="ignore").splitlines()

headers = [l for l in lines if l.startswith("## ") or l.startswith("### ")]
print(f"Total section headers: {len(headers)}")
for h in headers[:30]:
    print(" ", h)
