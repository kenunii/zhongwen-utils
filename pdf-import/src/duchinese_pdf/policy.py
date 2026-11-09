"""Shared defaults and annotation policy for desktop and tablet builds."""
import json
from importlib.resources import files

POLICY = json.loads(files('duchinese_pdf').joinpath('prompts/line_policy.json').read_text(encoding='utf-8'))
