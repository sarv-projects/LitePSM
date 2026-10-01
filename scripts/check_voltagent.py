import urllib.request
import re

url = "https://raw.githubusercontent.com/VoltAgent/awesome-agent-skills/main/README.md"
req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
with urllib.request.urlopen(req) as resp:
    text = resp.read().decode("utf-8", errors="ignore")

lines = text.splitlines()
print(f"VoltAgent lines: {len(lines)}")
headers = [l for l in lines if l.startswith("#")]
for h in headers[:20]:
    print(" ", h)

# sample items
items = [l for l in lines if l.strip().startswith("- [") or l.strip().startswith("* [")]
print(f"Found {len(items)} list items in VoltAgent")
for item in items[:10]:
    print("   ", item)
