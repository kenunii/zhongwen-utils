"""Line-ID annotation contract; derive substitutions locally, never repair output."""
import json
import copy
import unicodedata
from importlib.resources import files
from duchinese_pdf.annotate import make_request, compile_annotations, validate_schema
from duchinese_pdf.annotation_schema import SCHEMA, CONFUSABLE_GROUPS, obj, array, STRING

LINE_SCHEMA = obj({'lines': array(obj({'id': STRING, 'text': STRING, 'correction_reason': STRING})),
                   'sentences': SCHEMA['properties']['sentences']})
PROMPT = files('duchinese_pdf').joinpath('prompts/line_contract.txt').read_text(encoding='utf-8')


def payload(source):
    old = json.loads(make_request(source, 'unused')['input'][0]['content'])
    result = []
    for i, line in enumerate(old['lines']):
        start, text = line['start'], line['text']
        cuts = [0] + [p-start for p in old['gaps_before'] if start < p < start+len(text)] + [len(text)]
        result.append({'id': f'L{i+1}', 'parts': [text[a:b] for a,b in zip(cuts,cuts[1:])]})
    return {'lines': result, 'allowed_confusable_groups': list(CONFUSABLE_GROUPS)}


def messages(source):
    return [{'role':'system','content':PROMPT+'\nJSON schema:\n'+json.dumps(LINE_SCHEMA,ensure_ascii=False)},
            {'role':'user','content':json.dumps(payload(source),ensure_ascii=False,separators=(',',':'))}]


def compile_lines(source, answer, allowed_word_gaps=None):
    validate_schema(answer, LINE_SCHEMA)
    expected = payload(source)['lines']
    if [x['id'] for x in answer['lines']] != [x['id'] for x in expected]:
        raise ValueError('Missing, reordered, duplicated or unexpected line IDs')
    corrections, cursor = [], 0
    for original, output in zip(expected, answer['lines']):
        before = ''.join(original['parts']); after = output['text']
        if len(before) != len(after): raise ValueError(f"{original['id']}: inserted or deleted characters")
        changed = before != after
        if bool(output['correction_reason'].strip()) != changed:
            raise ValueError(f"{original['id']}: correction reason inconsistent with actual changes")
        for i,(a,b) in enumerate(zip(before,after)):
            if a != b:
                if not any(a in g and b in g for g in CONFUSABLE_GROUPS):
                    raise ValueError(f"{original['id']}: substitution outside allowed confusion groups")
                corrections.append({'offset':cursor+i,'before':a,'after':b,'reason':output['correction_reason']})
        cursor += len(before)
    # A model may add sentence-ending punctuation despite returning the exact
    # OCR lines. Remove only standalone, unannotated punctuation that cannot
    # match the next source character; never change source text or Chinese words.
    # The normal validator still checks the entire result and all geometry.
    text = ''.join(line['text'] for line in answer['lines'])
    sentences = copy.deepcopy(answer['sentences'])
    offset = 0
    removed = []
    for sid, sentence in enumerate(sentences):
        kept = []
        for index, word in enumerate(sentence['words']):
            token = word['text']
            if (len(token) == 1 and unicodedata.category(token).startswith('P')
                    and not word['pinyin'].strip() and not word['meaning'].strip()
                    and index == len(sentence['words'])-1
                    and text[offset:offset+1] != token):
                removed.append({'sentence':sid,'text':token,'source_offset':offset})
                continue
            kept.append(word)
            offset += len(token)
        sentence['words'] = kept
    corrected, annotations, audit = compile_annotations(source, {'corrections':corrections,'sentences':sentences},
                                                        allowed_word_gaps=allowed_word_gaps)
    if removed:
        audit['removed_extra_sentence_punctuation'] = removed
    return corrected, annotations, audit
