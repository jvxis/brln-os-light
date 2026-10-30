"""Exercise the actual standalone recovery verifier, without root/host mutation."""
import contextlib
import io
import json
from pathlib import Path
import sys
import tarfile
import tempfile
import unittest
from unittest import mock

SCRIPT = Path(__file__).resolve().parents[1] / 'recover-node-for-0.5.49.sh'
MARKER = 'python3 - "$stage" "$prefix" "$archive" <<\'PY\'\n'
TARGET = Path('/usr/lib/node_modules/corepack/package.json')
PAYLOAD = b'{"name":"corepack","version":"0.36.0"}\n'


class RecoveryInventory(unittest.TestCase):
    def verify(self, before, after, *, accepted, prefix='/usr', owner='nodejs', installed=PAYLOAD):
        code = SCRIPT.read_text().split(MARKER, 1)[1].split('\nPY\n', 1)[0]
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode='w') as output:
            member = tarfile.TarInfo('./usr/lib/node_modules/corepack/package.json')
            member.size = len(PAYLOAD)
            output.addfile(member, io.BytesIO(PAYLOAD))
        process = mock.Mock(stdout=io.BytesIO(archive.getvalue()))
        process.wait.return_value = 0
        process.poll.return_value = 0
        original_is_file = Path.is_file
        original_is_symlink = Path.is_symlink
        original_read_bytes = Path.read_bytes
        with tempfile.TemporaryDirectory() as temporary:
            stage = Path(temporary)
            for name, data in [('before', before), ('after', after)]:
                (stage / f'globals.{name}.json').write_text(json.dumps({
                    'dependencies': {key: {'version': version} for key, version in data.items()}
                }))
            with mock.patch.object(sys, 'argv', ['inventory', str(stage), prefix, '/verified-node.deb']), \
                 mock.patch('subprocess.Popen', return_value=process), \
                 mock.patch('subprocess.check_output', return_value=f'{owner}: {TARGET}\n'), \
                 mock.patch.object(Path, 'is_file', lambda p: True if p == TARGET else original_is_file(p)), \
                 mock.patch.object(Path, 'is_symlink', lambda p: False if p == TARGET else original_is_symlink(p)), \
                 mock.patch.object(Path, 'read_bytes', lambda p: installed if p == TARGET else original_read_bytes(p)), \
                 contextlib.redirect_stdout(io.StringIO()):
                try:
                    exec(compile(code, str(SCRIPT), 'exec'), {})
                except SystemExit as error:
                    if accepted:
                        raise
                    self.assertEqual(str(error), 'Global npm package inventory changed; review recovery record')
                else:
                    self.assertTrue(accepted, 'Unexpected inventory change accepted')

    def test_unchanged_and_bundled_npm_update(self):
        self.verify({'npm': '11.20.0', 'tool': '1.0'}, {'npm': '11.19.1', 'tool': '1.0'}, accepted=True)

    def test_reported_node26_to_24_corepack_addition(self):
        self.verify({'npm': '11.20.0', 'tool': '1.0'}, {'npm': '11.19.1', 'tool': '1.0', 'corepack': '0.36.0'}, accepted=True)

    def test_unverified_corepack_additions_rejected(self):
        self.verify({}, {'corepack': '0.99.0'}, accepted=False)
        self.verify({}, {'corepack': '0.36.0'}, prefix='/usr/local', accepted=False)
        self.verify({}, {'corepack': '0.36.0'}, owner='other-package', accepted=False)
        self.verify({}, {'corepack': '0.36.0'}, installed=b'changed package file', accepted=False)

    def test_existing_corepack_is_not_exempt(self):
        self.verify({'corepack': '0.35.0'}, {'corepack': '0.36.0'}, accepted=False)
        self.verify({'corepack': '0.36.0'}, {}, accepted=False)
        self.verify({'corepack': '0.36.0'}, {'corepack': '0.36.0'}, accepted=True)

    def test_other_app_changes_and_removal_rejected(self):
        self.verify({'tool': '1.0'}, {'tool': '2.0', 'corepack': '0.36.0'}, accepted=False)
        self.verify({'tool': '1.0'}, {'corepack': '0.36.0'}, accepted=False)


if __name__ == '__main__':
    unittest.main()
