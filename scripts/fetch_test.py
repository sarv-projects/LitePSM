import urllib.request
import json
import re

urls = {
    "punkpeye": "https://raw.githubusercontent.com/punkpeye/awesome-mcp-servers/main/README.md",
    "wong2": "https://raw.githubusercontent.com/wong2/awesome-mcp-servers/main/README.md",
    "mcp_official": "https://raw.githubusercontent.com/modelcontextprotocol/servers/main/README.md",
    "voltagent_skills": "https://raw.githubusercontent.com/VoltAgent/awesome-agent-skills/main/README.md"
}

for name, u in urls.items():
    try:
        req = urllib.request.Request(u, headers={"User-Agent": "Mozilla/5.0"})
        with urllib.request.urlopen(req, timeout=10) as resp:
            data = resp.read().decode('utf-8', errors='ignore')
            print(f"{name}: {len(data)} bytes")
            # find list items like - [Title](url) - description
            items = re.findall(r'-\s+\[([^\]]+)\]\((https?://[^\)]+)\)\s*(?:-|:|\u2013|\u2014)?\s*(.*)', data)
            print(f"  found {len(items)} items")
    except Exception as e:
        print(f"{name}: ERR {e}")
