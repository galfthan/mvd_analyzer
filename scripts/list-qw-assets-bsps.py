#!/usr/bin/env python3
"""list-qw-assets-bsps.py — write scripts/qw-assets-bsps.txt, the list of
every map in github.com/vikpe/qw-assets at one commit.

    python3 scripts/list-qw-assets-bsps.py [ref] > scripts/qw-assets-bsps.txt

`ref` is a branch, tag or commit (default main); the list names the commit
it resolved to, and fetch-bsps.sh downloads from that commit, so the list
and the files cannot drift apart. Each line is "<name> <git blob sha1>
<bytes>": GitHub's tree listing carries the blob hash, so the list is made
without downloading a map, and fetch-bsps.sh checks every file against it.

Run it again to pick up maps qw-assets has gained, and commit the result.
"""
import json
import re
import sys
import urllib.request

REPO = 'vikpe/qw-assets'
NAME = re.compile(r'maps/([^/]+)/\1\.bsp')


def get(path):
    req = urllib.request.Request(f'https://api.github.com/repos/{REPO}/{path}', headers={'User-Agent': 'list-qw-assets-bsps'})
    with urllib.request.urlopen(req, timeout=60) as resp:
        return json.load(resp)


def main():
    ref = sys.argv[1] if len(sys.argv) > 1 else 'main'
    commit = get(f'commits/{ref}')['sha']
    tree = get(f'git/trees/{commit}?recursive=1')
    if tree.get('truncated'):
        sys.exit('the tree listing came back truncated; the list would be incomplete')
    rows = []
    for entry in tree['tree']:
        m = NAME.fullmatch(entry['path'])
        if m and entry['type'] == 'blob':
            rows.append((m.group(1), entry['sha'], entry['size']))
    rows.sort()
    print(f'# Every map of github.com/{REPO} at the commit below: name, git blob sha1, bytes.')
    print('# Written by scripts/list-qw-assets-bsps.py; read by scripts/fetch-bsps.sh (set "all").')
    print(f'commit {commit}')
    for name, sha, size in rows:
        print(name, sha, size)


if __name__ == '__main__':
    main()
