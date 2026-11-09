"""Desktop imports stay separate and retry retains completed OCR and pages."""
import importlib.util
import json
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch
import pymupdf

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('desktop_library', ROOT/'scripts/desktop-library.py')
library = importlib.util.module_from_spec(spec)
spec.loader.exec_module(library)


class DesktopLibraryTests(unittest.TestCase):
    def test_pages_run_concurrently_and_are_all_saved(self):
        with tempfile.TemporaryDirectory() as directory:
            base=Path(directory); keys=base/'keys.json'
            keys.write_text(json.dumps({'vision_api_key':'test','deepseek_api_key':'test'})); keys.chmod(0o600)
            document=pymupdf.open()
            for _ in range(3): document.new_page()
            data=document.tobytes(); document.close()
            barrier=threading.Barrier(3, timeout=5)
            def ocr(image,key):
                barrier.wait()
                return {'fullTextAnnotation':{}}
            def annotate(number,image,raw,collection,title,key):
                page=collection/'pages'/f'{number:03d}'; page.mkdir(parents=True)
                (page/'book.json').write_text(json.dumps({'image_width':100,'image_height':100,'words':[],'sentences':[]}))
                return {'page':number,'image':str(image.relative_to(collection)),'status':'image-only','book':str((page/'book.json').relative_to(collection))}
            with patch.object(library,'LIBRARY',base/'books'), patch.object(library,'KEYS',keys), patch.dict('os.environ',{'CHINESE_BOOK_PARALLEL_PAGES':'3'}):
                book=library.import_pdf(data,'Parallel.pdf')
                with patch.object(library,'call_vision',ocr), patch.object(library,'process_page',annotate):
                    library.worker(book['id'])
                self.assertEqual(library.job(book['id'])['state'],'completed')
                self.assertEqual(library.job(book['id'])['done'],3)
                manifest=library.read(library.book_path(book['id'])/'collection/manifest.json')
                self.assertTrue(all(p.get('book') for p in manifest['pages']))

    def test_batch_size_and_parallel_limit(self):
        with tempfile.TemporaryDirectory() as directory:
            base=Path(directory); entries=[]
            for i in range(5):
                name=f'{i}.png'
                with (base/name).open('wb') as f: f.truncate(8_000_000)
                entries.append({'page':i+1,'image':name})
            groups=list(library.batches(entries,base,10))
            self.assertEqual([len(group) for group in groups],[2,2,1])
            self.assertEqual([len(group) for group in library.batches(entries,base,1)],[1]*5)

    def test_retry_preserves_completed_page_and_ocr(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory); keys = base/'keys.json'
            keys.write_text(json.dumps({'vision_api_key':'test','deepseek_api_key':'test'})); keys.chmod(0o600)
            document = pymupdf.open(); document.new_page(); document.new_page(); data = document.tobytes(); document.close()
            with patch.object(library,'LIBRARY',base/'books'), patch.object(library,'KEYS',keys):
                book = library.import_pdf(data,'Test.pdf'); book_id = book['id']; folder = library.book_path(book_id)
                calls=[]
                def ocr(image,key):
                    calls.append(image.name)
                    if image.name == 'page-002.png' and calls.count(image.name) == 1: raise RuntimeError('temporary OCR error')
                    return {'fullTextAnnotation':{}}
                def annotate(number,image,raw,collection,title,key):
                    page=collection/'pages'/f'{number:03d}'; page.mkdir(parents=True)
                    book_path=page/'book.json'; book_path.write_text(json.dumps({'image_width':100,'image_height':100,'words':[],'sentences':[]}))
                    return {'page':number,'image':str(image.relative_to(collection)),'status':'image-only','book':str(book_path.relative_to(collection))}
                with patch.object(library,'call_vision',ocr), patch.object(library,'process_page',annotate):
                    library.worker(book_id)
                    self.assertEqual(library.job(book_id)['state'],'error')
                    self.assertEqual(library.job(book_id)['done'],1)
                    library.worker(book_id)
                self.assertEqual(library.job(book_id)['state'],'completed')
                self.assertEqual(calls.count('page-001.png'),1)
                self.assertEqual(calls.count('page-002.png'),2)
                self.assertEqual((folder/'source.pdf').read_bytes(),data)
                manifest=library.read(folder/'collection/manifest.json')
                self.assertEqual(len([p for p in manifest['pages'] if p.get('book')]),2)


if __name__ == '__main__': unittest.main()
