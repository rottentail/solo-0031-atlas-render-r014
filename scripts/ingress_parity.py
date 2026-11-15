#!/usr/bin/env python3
"""Prove the two asset ingress paths follow one shared admission rule.

The direct path is `PUT /v1/assets/{id}`: one asset per request.
The packaged path is `POST /v1/imports/{id}` with an exported `atlas-scene`
package: a scene plus its whole asset closure in one atomic commit.

The rule under test is independent of either transport:

    unused id                  -> accept, content becomes available
    used id, identical content -> accept the existing record, no new asset,
                                  only while it is still available
    used id, other content     -> conflict
    used id, retired           -> conflict, even for identical content

This script states that rule once in Python, predicts both outcomes from it,
and then checks each server against the prediction. The paths intentionally
differ in commit scope: a direct rejection affects one identifier, while a
packaged conflict rejects the whole package and leaves no asset behind. That
difference is asserted as well, so the shared rule is proven to be shared
without collapsing the two entry points into one.

Assertions use public endpoints only: `PUT /v1/assets/{id}`,
`POST /v1/assets/{id}/retire`, `POST /v1/imports/{id}`, `GET /v1/status`, and
the scene/snapshot routes used to export a package.

Run: python3 scripts/ingress_parity.py
"""
import json
import os
import socket
import struct
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import zlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

ACCEPT, REUSE, CONFLICT = 'accept', 'reuse', 'conflict'


def png_bytes(rows):
    """Build a 2x2 RGBA PNG from two RGBA tuples, one per pixel row."""
    def chunk(tag, data):
        return struct.pack('>I', len(data)) + tag + data + struct.pack('>I', zlib.crc32(tag + data))
    body = b''.join(b'\0' + bytes(px * 2) for px in rows)
    return b'\x89PNG\r\n\x1a\n' + \
        chunk(b'IHDR', struct.pack('>IIBBBBB', 2, 2, 8, 6, 0, 0, 0)) + \
        chunk(b'IDAT', zlib.compress(body)) + chunk(b'IEND', b'')


CONTENT = {
    'a': png_bytes([(240, 40, 80, 255), (40, 80, 240, 128)]),
    'b': png_bytes([(0, 0, 0, 255), (255, 255, 255, 255)]),
    'c': png_bytes([(10, 200, 30, 255), (5, 5, 5, 64)]),
}


# --- The rule, stated once, without reference to HTTP or packages ---

def admit(seed, incoming):
    """Predict a decision per incoming asset id.

    seed     {asset-id: (content-key, retired)}
    incoming {asset-id: content-key}
    returns  {asset-id: ACCEPT | REUSE | CONFLICT}
    """
    decisions = {}
    for asset_id, key in incoming.items():
        if asset_id not in seed:
            decisions[asset_id] = ACCEPT
            continue
        seeded_key, retired = seed[asset_id]
        if retired or seeded_key != key:
            decisions[asset_id] = CONFLICT
        else:
            decisions[asset_id] = REUSE
    return decisions


def predict_direct(seed, incoming):
    """Direct path: one request per id, count after all requests."""
    decisions = admit(seed, incoming)
    statuses = {asset_id: 201 if d != CONFLICT else 409
                for asset_id, d in decisions.items()}
    count = len(seed) + sum(1 for d in decisions.values() if d == ACCEPT)
    return statuses, count


def predict_package(seed, incoming):
    """Packaged path: one atomic commit, count after the commit."""
    decisions = admit(seed, incoming)
    if CONFLICT in decisions.values():
        return 409, len(seed)
    return 201, len(seed) + sum(1 for d in decisions.values() if d == ACCEPT)


# --- Transport ---

class Server:
    def __init__(self, binary, directory):
        self.directory = Path(directory)
        self.binary = binary
        self.proc = None
        self.log = open(self.directory / 'server.log', 'ab')
        self.start()

    def start(self):
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        self.url = 'http://127.0.0.1:%d' % port
        env = dict(os.environ, ATLAS_ADDR=self.url.split('//')[1],
                   ATLAS_STATE=str(self.directory / 'state.json'))
        self.proc = subprocess.Popen([str(self.binary)], env=env,
                                     stdout=self.log, stderr=self.log)
        for _ in range(200):
            try:
                self.call('GET', '/healthz')
                return
            except (urllib.error.URLError, ConnectionError):
                if self.proc.poll() is not None:
                    raise RuntimeError((self.directory / 'server.log').read_text())
                time.sleep(.02)
        raise RuntimeError('server startup deadline exceeded')

    def stop(self):
        if self.proc and self.proc.poll() is None:
            self.proc.terminate()
            self.proc.wait(timeout=15)

    def call(self, method, path, body=None):
        headers = {}
        if isinstance(body, (dict, list)):
            body = json.dumps(body).encode()
            headers['Content-Type'] = 'application/json'
        request = urllib.request.Request(self.url + path, data=body,
                                         method=method, headers=headers)
        try:
            response = urllib.request.urlopen(request, timeout=15)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            data = response.read()
            payload = None
            if response.headers.get('Content-Type', '').startswith('application/json'):
                payload = json.loads(data)
            return response.status, payload

    def expect(self, method, path, body=None, status=200):
        got, payload = self.call(method, path, body)
        if got != status:
            raise RuntimeError('%s %s: expected %d, received %d'
                               % (method, path, status, got))
        return payload

    def asset_count(self):
        return self.expect('GET', '/v1/status')['assetCount']


def doc_referencing(asset_ids):
    return {"width": 8, "height": 8, "background": "#FFFFFF", "layers": [
        {"id": 'bitmap-%d' % i, "kind": "image", "width": 8, "height": 8,
         "assetId": aid, "transform": {"scaleX": 1, "scaleY": 1}, "opacity": 1}
        for i, aid in enumerate(asset_ids)]}


def export_package(source, assets):
    """Publish `assets` as {asset-id: content-key} and export one package."""
    for asset_id, key in assets.items():
        source.expect('PUT', '/v1/assets/%s' % asset_id, CONTENT[key], 201)
    source.expect('PUT', '/v1/scenes/package-src', {
        'name': 'package source', 'expectedRevision': 0,
        'document': doc_referencing(sorted(assets))})
    snap = source.expect('POST', '/v1/scenes/package-src/snapshots',
                         {'expectedRevision': 1}, 201)
    return source.expect('GET', '/v1/snapshots/%s/package' % snap['id'])


def seed(server, spec):
    """Apply a destination seed of {asset-id: (content-key, retired)}."""
    for asset_id, (key, retired) in spec.items():
        server.expect('PUT', '/v1/assets/%s' % asset_id, CONTENT[key], 201)
        if retired:
            server.expect('POST', '/v1/assets/%s/retire' % asset_id)


CASES = [
    ('unused id accepts content', {}, {'fresh': 'a'}),
    ('same id same content is reused', {'shared': ('a', False)}, {'shared': 'a'}),
    ('same id different content conflicts', {'shared': ('a', False)}, {'shared': 'b'}),
    ('retired id rejects identical content', {'shared': ('a', True)}, {'shared': 'a'}),
    ('retired id rejects different content', {'shared': ('a', True)}, {'shared': 'b'}),
    ('closure reuses one asset and adds one', {'shared': ('a', False)},
     {'shared': 'a', 'extra': 'c'}),
    ('closure conflict rejects the whole package', {'shared': ('a', False)},
     {'shared': 'b', 'extra': 'c'}),
]


def run_case(binary, name, seed_spec, incoming):
    want_direct = predict_direct(seed_spec, incoming)
    want_package = predict_package(seed_spec, incoming)
    with tempfile.TemporaryDirectory(prefix='atlas-parity-') as root:
        root = Path(root)
        for child in ('source', 'direct', 'packaged'):
            (root / child).mkdir()
        source = Server(binary, root / 'source')
        direct = Server(binary, root / 'direct')
        packaged = Server(binary, root / 'packaged')
        try:
            package = export_package(source, incoming)

            seed(direct, seed_spec)
            got_direct = {}
            for asset_id, key in incoming.items():
                got_direct[asset_id], _ = direct.call(
                    'PUT', '/v1/assets/%s' % asset_id, CONTENT[key])
            direct_count = direct.asset_count()

            seed(packaged, seed_spec)
            package_status, _ = packaged.call(
                'POST', '/v1/imports/packaged',
                {'name': 'imported', 'package': package})
            package_count = packaged.asset_count()

            checks = {
                'direct_status_matches_rule': got_direct == want_direct[0],
                'direct_count_matches_rule': direct_count == want_direct[1],
                'package_status_matches_rule': package_status == want_package[0],
                'package_count_matches_rule': package_count == want_package[1],
            }
            return {'case': name,
                    'rule': admit(seed_spec, incoming),
                    'direct': {'status': got_direct, 'count': direct_count},
                    'package': {'status': package_status, 'count': package_count},
                    'checks': checks, 'ok': all(checks.values())}
        finally:
            source.stop()
            direct.stop()
            packaged.stop()


def run_atomic_rejection(binary):
    """A rejected package leaves no asset, not even the admissible one."""
    with tempfile.TemporaryDirectory(prefix='atlas-atomic-') as root:
        root = Path(root)
        for child in ('source', 'target'):
            (root / child).mkdir()
        source = Server(binary, root / 'source')
        target = Server(binary, root / 'target')
        try:
            package = export_package(source, {'shared': 'b', 'extra': 'c'})
            seed(target, {'shared': ('a', False)})
            before = target.asset_count()
            status, _ = target.call('POST', '/v1/imports/atomic',
                                    {'name': 'atomic', 'package': package})
            after = target.asset_count()
            scene_status, _ = target.call('GET', '/v1/scenes/atomic')
            checks = {
                'rejected': status == 409,
                'no_asset_added': before == after,
                'scene_absent': scene_status == 404,
            }
            return {'case': 'rejected package adds no asset',
                    'status': status, 'assets_before': before,
                    'assets_after': after, 'checks': checks,
                    'ok': all(checks.values())}
        finally:
            source.stop()
            target.stop()


def main():
    with tempfile.TemporaryDirectory(prefix='atlas-parity-build-') as build:
        binary = Path(build) / 'atlas'
        subprocess.run(['go', 'build', '-o', str(binary), './cmd/atlas'],
                       cwd=ROOT, check=True, timeout=120)
        results = [run_case(binary, *case) for case in CASES]
        results.append(run_atomic_rejection(binary))
        for result in results:
            print(json.dumps(result, ensure_ascii=False))
        failed = [r['case'] for r in results if not r['ok']]
        if failed:
            print('PARITY FAILED: ' + ', '.join(failed))
            return 1
        print('PARITY OK: %d cases agree with the shared rule on both paths'
              % len(results))
        return 0


if __name__ == '__main__':
    raise SystemExit(main())
