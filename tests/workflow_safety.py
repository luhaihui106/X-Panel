"""Fail closed if inherited publishing workflows become automatic again."""
from pathlib import Path
import yaml

for name in ('release.yml', 'docker.yml'):
    data = yaml.load(Path('.github/workflows', name).read_text(), Loader=yaml.BaseLoader)
    assert set(data['on']) == {'workflow_dispatch'}, (name, data['on'])
    assert data.get('jobs'), name
print('PASS: release and Docker workflows are explicit-manual only; no push/tag/release triggers')
