import urllib.request
import re

url = "https://raw.githubusercontent.com/VoltAgent/awesome-agent-skills/main/README.md"
req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
with urllib.request.urlopen(req) as resp:
    text = resp.read().decode("utf-8", errors="ignore")

skills = re.findall(r'-\s+\*\*\[([^\]]+)\]\((https?://[^\)]+)\)\*\*\s*-\s*([^\n\r]+)', text)
print(f"Parsed {len(skills)} real skills from VoltAgent!")
for s in skills[:15]:
    print("  ", s[0], "->", s[2])
