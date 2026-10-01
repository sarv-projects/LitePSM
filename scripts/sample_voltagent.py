import urllib.request

url = "https://raw.githubusercontent.com/VoltAgent/awesome-agent-skills/main/README.md"
req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
with urllib.request.urlopen(req) as resp:
    lines = resp.read().decode("utf-8", errors="ignore").splitlines()

for i, l in enumerate(lines[80:150], start=81):
    print(f"{i}: {l}")
