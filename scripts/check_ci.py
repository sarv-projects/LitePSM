import urllib.request
import json
import sys

sys.stdout.reconfigure(encoding='utf-8')

url = 'https://api.github.com/repos/sarv-projects/LitePSM/actions/runs?per_page=1'
req = urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0'})
with urllib.request.urlopen(req, timeout=10) as resp:
    data = json.load(resp)
    r = data['workflow_runs'][0]
    jobs_url = r['jobs_url']

jreq = urllib.request.Request(jobs_url, headers={'User-Agent': 'Mozilla/5.0'})
with urllib.request.urlopen(jreq, timeout=10) as jresp:
    jdata = json.load(jresp)
    for j in jdata['jobs']:
        if j['conclusion'] == 'failure':
            job_id = j['id']
            print(f"Failed Job: {j['name']} (id: {job_id})")
            log_url = f"https://api.github.com/repos/sarv-projects/LitePSM/actions/jobs/{job_id}/logs"
            try:
                lreq = urllib.request.Request(log_url, headers={'User-Agent': 'Mozilla/5.0'})
                with urllib.request.urlopen(lreq, timeout=15) as lresp:
                    log_text = lresp.read().decode('utf-8', errors='ignore')
                    # print lines around FAIL
                    lines = log_text.splitlines()
                    for idx, line in enumerate(lines):
                        if 'FAIL:' in line or '--- FAIL' in line or 'panic:' in line or 'error:' in line:
                            start = max(0, idx - 5)
                            end = min(len(lines), idx + 20)
                            print(f"--- Log around line {idx} ---")
                            for l in lines[start:end]:
                                print("  ", l)
                            break
            except Exception as e:
                print(f"Could not fetch log directly (might require auth or redirect): {e}")
            break
