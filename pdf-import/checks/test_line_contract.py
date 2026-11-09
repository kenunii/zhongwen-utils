"""Regression checks for the experimental model-independent alignment contract."""
import copy
import json
import subprocess
import sys
from pathlib import Path
import unittest
sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'experiments'))
from line_contract import compile_lines

class LineContractTests(unittest.TestCase):
    def setUp(self):
        self.source={'image_size':[200,100],'characters':[{'offset':i,'text':c,'line':0,'box':[i*20,0,20,20]} for i,c in enumerate('自已。')]}
        self.answer={'lines':[{'id':'L1','text':'自己。','correction_reason':'自己 is the intended word.'}],
                     'sentences':[{'translation':'Oneself.','words':[{'text':'自己','pinyin':'zì jǐ','meaning':'oneself'},{'text':'。','pinyin':'','meaning':''}]}]}
    def test_derives_position_and_preserves_geometry(self):
        before=copy.deepcopy(self.source)
        corrected,_,audit=compile_lines(self.source,self.answer)
        self.assertEqual(audit['changes'][0]['offset'],1)
        self.assertEqual(self.source,before)
        self.assertEqual([c['box'] for c in corrected['characters']],[c['box'] for c in before['characters']])
    def test_rejects_bad_line_coverage(self):
        for lines in [[],self.answer['lines']*2,[dict(self.answer['lines'][0],id='L2')]]:
            with self.subTest(lines=lines),self.assertRaises(ValueError):compile_lines(self.source,dict(self.answer,lines=lines))
    def test_rejects_insert_delete_and_disallowed_substitution(self):
        for text in ['自[blank]己。','自。','自己！']:
            a=copy.deepcopy(self.answer);a['lines'][0]['text']=text
            with self.subTest(text=text),self.assertRaises(ValueError):compile_lines(self.source,a)
    def test_rejects_word_coverage_even_when_lines_correct(self):
        a=copy.deepcopy(self.answer);a['sentences'][0]['words'].pop()
        with self.assertRaises(ValueError):compile_lines(self.source,a)
    def test_rejects_reason_without_change(self):
        a=copy.deepcopy(self.answer);a['lines'][0]['text']='自已。'
        with self.assertRaises(ValueError):compile_lines(self.source,a)
    def test_removes_only_extra_unannotated_sentence_punctuation(self):
        a=copy.deepcopy(self.answer)
        a['sentences'][0]['words'].append({'text':'。','pinyin':'','meaning':''})
        corrected, annotations, audit = compile_lines(self.source,a)
        self.assertEqual(''.join(c['text'] for c in corrected['characters']),'自己。')
        self.assertEqual(''.join(w['hanzi'] for w in annotations['words']),'自己。')
        self.assertEqual(audit['removed_extra_sentence_punctuation'][0]['source_offset'],3)
        self.assertEqual(len(a['sentences'][0]['words']),3)
    def test_still_rejects_inserted_words_or_annotated_punctuation(self):
        for extra in [{'text':'好','pinyin':'hǎo','meaning':'good'},
                      {'text':'。','pinyin':'x','meaning':'not empty'}]:
            a=copy.deepcopy(self.answer);a['sentences'][0]['words'].append(extra)
            with self.subTest(extra=extra),self.assertRaises(ValueError):compile_lines(self.source,a)
    def test_shared_tablet_desktop_cases(self):
        root=Path(__file__).resolve().parents[2]
        for case in json.loads((root/'processor/testdata/annotation_cases.json').read_text()):
            with self.subTest(case=case['name']):
                text=case['text']
                source={'image_size':[1000,100], 'characters':[{'offset':i,'text':c,'line':0,'box':[i*20,0,20,20]} for i,c in enumerate(text)]}
                answer={'lines':[{'id':'L1','text':text,'correction_reason':''}], 'sentences':case['sentences']}
                if case['valid']:
                    _, annotations, _ = compile_lines(source,answer)
                    self.assertEqual(annotations['sentences'][0]['text'],text)
                else:
                    with self.assertRaises(ValueError): compile_lines(source,answer)
    def test_tablet_embedded_contract_is_current(self):
        root=Path(__file__).resolve().parents[2]
        subprocess.run([sys.executable,str(root/'scripts/sync-processor-contract.py'),'--check'],check=True)
