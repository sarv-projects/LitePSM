import urllib.request
import re
import json

sources = [
    ("punkpeye", "https://raw.githubusercontent.com/punkpeye/awesome-mcp-servers/main/README.md"),
    ("wong2", "https://raw.githubusercontent.com/wong2/awesome-mcp-servers/main/README.md"),
    ("voltagent", "https://raw.githubusercontent.com/VoltAgent/awesome-agent-skills/main/README.md"),
    ("modelcontextprotocol", "https://raw.githubusercontent.com/modelcontextprotocol/servers/main/README.md")
]

for name, url in sources:
    try:
        req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
        with urllib.request.urlopen(req, timeout=10) as resp:
            text = resp.read().decode("utf-8", errors="ignore")
            # find markdown links
            items = re.findall(r'-\s+\[([^\]]+)\]\((https?://github\.com/[^\)]+)\)(?:\s*(?:[-–—:]\s*)?([^\n\r]+))?', text)
            print(f"{name}: {len(text)} bytes, parsed {len(items)} repo items")
            if items:
                print("  Sample 1:", items[0])
                if len(items) > 1:
                    print("  Sample 2:", items[1])
    except Exception as e:
        print(f"{name}: error {e}")
