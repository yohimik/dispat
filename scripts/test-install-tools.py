#!/usr/bin/env python3
"""Exercise the scoped bootstrap through the real dispat config resolver."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class ToolInstallTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="dispat tooling ' space ")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        source = Path(__file__).resolve().parent.parent
        for file in ('tools/bootstrap.yaml', 'scripts/install-tools.sh'):
            target = self.root / file
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source / file, target)
        shutil.copytree(source / '.aqua', self.root / '.aqua')
        toolchain = self.root / 'toolchain'
        for folder in ('bin', 'lib', 'src'):
            (toolchain / folder).mkdir(parents=True)
        self.log = self.root / 'calls'
        self.aqua = self.root / 'aqua-mock'
        self.aqua.write_text('''#!/bin/sh
set -eu
printf '%s\\n' "$*" >> "$TOOL_TEST_LOG"
[ "$AQUA_POLICY_CONFIG" = "$TOOL_TEST_ROOT/.aqua/aqua-policy.yaml" ]
case "$3" in
  install) test "${TOOL_TEST_FAIL:-}" != install ;;
  cp) touch "$7/crier" ;;
  which) printf '%s/toolchain/bin/tinygo\\n' "$TOOL_TEST_ROOT" ;;
  *) exit 31 ;;
esac
''')
        self.aqua.chmod(0o755)
        self.dispat = self.root / 'dispat-mock'
        self.dispat.write_text('''#!/bin/sh
set -eu
if [ "$1" != install ]; then exec "$TOOL_TEST_DISPAT" "$@"; fi
printf '%s\\n' "$*" >> "$TOOL_TEST_LOG"
[ "$2" = aquaproj/aqua ] && [ "$3" = --release ] && [ "$4" = v2.63.0 ]
shift 4
while [ "$#" -gt 0 ]; do
  if [ "$1" = --bin-dir ]; then cp "$TOOL_TEST_AQUA" "$2/aqua"; exit; fi
  shift
done
exit 32
''')
        self.dispat.chmod(0o755)
        self.env = {**os.environ, 'DISPAT_BIN': str(self.dispat),
                    'TOOL_TEST_DISPAT': os.environ.get('DISPAT_TEST_BINARY', shutil.which('dispat') or 'dispat'),
                    'TOOL_TEST_ROOT': str(self.root), 'TOOL_TEST_AQUA': str(self.aqua),
                    'TOOL_TEST_LOG': str(self.log)}

    def run_install(self, tool, destination='output tools', **env):
        return subprocess.run(['sh', str(self.root / 'scripts/install-tools.sh'), tool, destination],
                              cwd=self.root, env={**self.env, **env}, capture_output=True, text=True)

    def test_all_tools_in_minimal_non_git_context(self):
        result = self.run_install('all')
        self.assertEqual(result.returncode, 0, result.stderr)
        destination = self.root / 'output tools'
        self.assertTrue((destination / 'crier').is_file())
        self.assertEqual((destination / 'tinygo').resolve(), self.root / 'toolchain')
        self.assertIn('--release v2.63.0', self.log.read_text())

    def test_selected_tool_and_failure_stop(self):
        result = self.run_install('crier', TOOL_TEST_FAIL='install')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('install --tags crier', self.log.read_text())
        self.assertFalse((self.root / 'output tools/crier').exists())

    def test_tinygo_preserves_existing_directory(self):
        target = self.root / 'output tools/tinygo'
        target.mkdir(parents=True)
        (target / 'keep').write_text('original')
        result = self.run_install('tinygo')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('refusing to replace', result.stderr)
        self.assertEqual((target / 'keep').read_text(), 'original')

    def test_invalid_selection_has_no_side_effects(self):
        result = self.run_install('unknown')
        self.assertEqual(result.returncode, 2)
        self.assertFalse(self.log.exists())
        self.assertFalse((self.root / 'output tools').exists())

    def test_empty_destination_is_refused(self):
        result = self.run_install('crier', destination='')
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn('empty destination', result.stderr)
        self.assertFalse(self.log.exists())

    def test_empty_bin_dir_is_refused(self):
        result = subprocess.run(['sh', str(self.root / 'scripts/install-tools.sh'), 'crier'],
                                cwd=self.root, env={**self.env, 'DISPAT_BIN_DIR': ''},
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn('DISPAT_BIN_DIR is set but empty', result.stderr)
        self.assertFalse(self.log.exists())

    def test_config_refuses_an_empty_destination_variable(self):
        """A caller reaching the config directly gets the wrapper's answer."""
        result = subprocess.run(
            [self.env['TOOL_TEST_DISPAT'], '--root', str(self.root),
             '--config', 'tools/bootstrap.yaml', 'exec', 'install-tools', '--in', 'root'],
            cwd=self.root, env={**self.env, 'DISPAT_TOOL_DESTINATION': ''},
            capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('DISPAT_TOOL_DESTINATION is set but empty',
                      result.stdout + result.stderr)
        self.assertFalse(self.log.exists())


if __name__ == '__main__':
    unittest.main()
