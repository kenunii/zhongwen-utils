"""Strict API schema and deterministic substitution policy."""
from .policy import POLICY

# Shared source for the desktop and the generated standalone tablet binary.
CONFUSABLE_GROUPS = tuple(POLICY['confusable_groups'])


def obj(properties):
    return {'type': 'object', 'properties': properties,
            'required': list(properties), 'additionalProperties': False}


def array(items):
    return {'type': 'array', 'items': items}


STRING = {'type': 'string'}
SCHEMA = obj({
    'corrections': array(obj({'offset': {'type': 'integer'}, 'before': STRING,
                              'after': STRING, 'reason': STRING})),
    'sentences': array(obj({
        'translation': STRING,
        'words': array(obj({'text': STRING, 'pinyin': STRING, 'meaning': STRING})),
    })),
})
