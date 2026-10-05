"""Report the status of the most recent GitHub Actions CI workflow run.

Exit codes:
    0 - the latest run (and its jobs) succeeded
    1 - the latest run failed or errored
    2 - the status could not be determined (network, API, or parse error)

The run query is scoped to the ``ci.yml`` workflow file so a newer run of any
other workflow (for example a Release run) can never mask a red CI run.

Uses only the Python standard library. When the GITHUB_TOKEN environment
variable is set it is sent as an ``Authorization: Bearer`` header, which
avoids anonymous API rate limits and grants access to log downloads.
"""

import json
import os
import sys
import urllib.error
import urllib.request

sys.stdout.reconfigure(encoding='utf-8')

# Workflow-scoped endpoint: GET /repos/{owner}/{repo}/actions/workflows/{workflow_id}/runs
# accepts the workflow file name (or numeric id) as {workflow_id}; scoping here
# is what guarantees the newest *CI* run, not the newest run of any workflow.
RUNS_URL = (
    'https://api.github.com/repos/sarv-projects/LiteSPM'
    '/actions/workflows/ci.yml/runs?per_page=1'
)
FAILED_CONCLUSIONS = {
    'failure',
    'errored',
    'error',
    'timed_out',
    'cancelled',
    'action_required',
    'startup_failure',
}


def build_headers():
    headers = {
        'User-Agent': 'Mozilla/5.0',
        'Accept': 'application/vnd.github+json',
    }
    token = os.environ.get('GITHUB_TOKEN')
    if token:
        headers['Authorization'] = f'Bearer {token}'
    return headers


def fetch_json(url, timeout=10):
    """Fetch a URL and decode its JSON body. Raises on request/parse errors."""
    req = urllib.request.Request(url, headers=build_headers())
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        data = json.load(resp)
    if not isinstance(data, dict):
        raise ValueError(f'expected a JSON object from {url}, got {type(data).__name__}')
    return data


def print_failure_logs(job_id):
    """Print log excerpts around FAIL markers for a failed job, best effort."""
    log_url = (
        'https://api.github.com/repos/sarv-projects/LiteSPM'
        f'/actions/jobs/{job_id}/logs'
    )
    try:
        req = urllib.request.Request(log_url, headers=build_headers())
        with urllib.request.urlopen(req, timeout=15) as lresp:
            log_text = lresp.read().decode('utf-8', errors='ignore')
        lines = log_text.splitlines()
        for idx, line in enumerate(lines):
            if 'FAIL:' in line or '--- FAIL' in line or 'panic:' in line or 'error:' in line:
                start = max(0, idx - 5)
                end = min(len(lines), idx + 20)
                print(f'--- Log around line {idx} ---')
                for excerpt in lines[start:end]:
                    print('  ', excerpt)
                break
    except Exception as e:
        print(f'Could not fetch log directly (might require auth or redirect): {e}')


def main():
    try:
        data = fetch_json(RUNS_URL)
    except (urllib.error.URLError, ValueError, OSError, TimeoutError) as e:
        print(f'Could not fetch workflow runs: {e}', file=sys.stderr)
        return 2

    runs = data.get('workflow_runs')
    if not isinstance(runs, list) or not runs:
        print('No usable workflow runs in the API response '
              '(workflow_runs missing, empty, or not a list).',
              file=sys.stderr)
        return 2

    run = runs[0]
    if not isinstance(run, dict):
        print(f'Unexpected workflow run payload shape: {type(run).__name__}.',
              file=sys.stderr)
        return 2

    conclusion = run.get('conclusion')
    status = run.get('status')
    run_id = run.get('id')
    run_name = run.get('name', '<unknown>')

    jobs_failed = False
    jobs_url = run.get('jobs_url')
    if jobs_url:
        try:
            jdata = fetch_json(jobs_url)
        except (urllib.error.URLError, ValueError, OSError, TimeoutError) as e:
            print(f'Could not fetch jobs for run {run_id}: {e}', file=sys.stderr)
            return 2
        jobs = jdata.get('jobs')
        if not isinstance(jobs, list) or any(not isinstance(j, dict) for j in jobs):
            print(f'Unexpected jobs payload shape for run {run_id}.',
                  file=sys.stderr)
            return 2
        for job in jobs:
            if job.get('conclusion') in FAILED_CONCLUSIONS:
                jobs_failed = True
                print(f"Failed Job: {job.get('name')} (id: {job.get('id')})")
                print_failure_logs(job.get('id'))

    if conclusion in FAILED_CONCLUSIONS or jobs_failed:
        print(f'Run {run_id} ({run_name}) did not pass: '
              f'conclusion={conclusion} status={status}')
        return 1

    if conclusion == 'success':
        print(f'Run {run_id} ({run_name}) succeeded.')
        return 0

    # conclusion is None (still running) or a non-failure verdict we cannot
    # treat as success: report as undetermined rather than pass silently.
    print(f'Run {run_id} ({run_name}) has no success verdict yet: '
          f'conclusion={conclusion} status={status}', file=sys.stderr)
    return 2


if __name__ == '__main__':
    sys.exit(main())
