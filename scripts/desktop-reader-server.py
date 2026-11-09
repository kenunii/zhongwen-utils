#!/usr/bin/env python3
"""Loopback-only reader and local PDF annotation persistence."""
import argparse
import hashlib
import importlib.util
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import mimetypes
import math
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import tempfile
from urllib.parse import urlsplit
import pymupdf

spec = importlib.util.spec_from_file_location('desktop_library', Path(__file__).with_name('desktop-library.py'))
library = importlib.util.module_from_spec(spec)
spec.loader.exec_module(library)


def serve(collection, source, frontend, output, state, port):
    manifest = json.loads((collection/'manifest.json').read_text())
    if hashlib.sha256(source.read_bytes()).hexdigest() != manifest['pdf_sha256']:
        raise ValueError('Source PDF does not match this textbook')
    state.mkdir(parents=True, exist_ok=True, mode=0o700)
    notes_path = state/'textboxes.json'
    notes = json.loads(notes_path.read_text()) if notes_path.exists() else []
    progress_path = state/'reading-position.json'
    progress = json.loads(progress_path.read_text()) if progress_path.exists() else None
    token = secrets.token_urlsafe(32)
    if port == 8767:
        library.atomic(library.LIBRARY/'main.json', {'id':manifest['pdf_sha256'],'title':manifest['title'],'port':port})
    images = {'/'+collection.name+'/'+e['image']:collection/e['image'] for e in manifest['pages']}
    dimensions = {}
    for e in manifest['pages']:
        if e.get('book'):
            dimensions[e['page']] = json.loads((collection/e['book']).read_text())['image_width']
        else:
            from PIL import Image
            with Image.open(collection/e['image']) as image:
                dimensions[e['page']] = image.width
    output.parent.mkdir(parents=True, exist_ok=True)

    def persist(note):
        updated = [n for n in notes if n['id'] != note['id']]
        if note['text'].strip(): updated.append(note)
        fd, name = tempfile.mkstemp(suffix='.pdf', dir=output.parent)
        os.close(fd)
        target = Path(name)
        try:
            shutil.copyfile(output if output.exists() else source, target)
            with pymupdf.open(target) as doc:
                # Replace this app's textbox with its latest content.
                for page_num in {n['page'] for n in notes if n['id']==note['id']} | {note['page']}:
                    page = doc[page_num-1]
                    for annot in list(page.annots() or []):
                        if annot.info.get('subject') == 'ChineseBook:'+note['id']:
                            page.delete_annot(annot)
                if note['text'].strip():
                    page = doc[note['page']-1]
                    w,h = page.rect.width,page.rect.height
                    rect = pymupdf.Rect(note['x']*w,note['y']*h,
                        min(w,(note['x']+note['w'])*w),min(h,(note['y']+note['h'])*h))
                    size = note['font_size']*w/dimensions[note['page']]
                    annot = page.add_freetext_annot(rect,note['text'],
                        fontsize=size,text_color=(.14,.30,.44),border_width=0)
                    annot.set_info(content=note['text'],subject='ChineseBook:'+note['id'],title='Chinese Book')
                doc.saveIncr()
            os.replace(target,output)
            temp = notes_path.with_suffix('.tmp')
            temp.write_text(json.dumps(updated,ensure_ascii=False))
            os.replace(temp,notes_path)
            notes[:] = updated
        finally:
            target.unlink(missing_ok=True)

    class Handler(BaseHTTPRequestHandler):
        def log_message(self,*args): pass
        def send(self,code,body,kind='application/json'):
            self.send_response(code); self.send_header('Content-Type',kind)
            self.send_header('Cache-Control','no-store'); self.end_headers(); self.wfile.write(body)
        def do_GET(self):
            if self.headers.get('Host') not in {f'127.0.0.1:{port}',f'localhost:{port}'}:
                self.send(403,b'{}');return
            path=urlsplit(self.path).path
            if path=='/api/notes':
                self.send(200,json.dumps(dict(notes=notes,token=token,pdf=str(output)),ensure_ascii=False).encode());return
            if path=='/api/progress':
                self.send(200,json.dumps(dict(position=progress,token=token)).encode());return
            if path=='/api/health':
                self.send(200,json.dumps(dict(book=manifest['pdf_sha256'],version=2)).encode());return
            if path=='/api/books':
                main=library.read(library.LIBRARY/'main.json')
                books=([main] if main else []) + [b for b in library.catalog() if not main or b['id'] != main['id']]
                self.send(200,json.dumps(dict(books=books),ensure_ascii=False).encode());return
            if path=='/api/job':
                book_id=urlsplit(self.path).query
                try: self.send(200,json.dumps(library.job(book_id)).encode())
                except ValueError: self.send(400,b'{}')
                return
            file = frontend/({'/':'index.html'}.get(path,path.lstrip('/'))) if path in ['/', '/index.html','/reader.js','/book.js','/notes.js'] else images.get(path)
            if file and file.is_file():
                self.send(200,file.read_bytes(),mimetypes.guess_type(file.name)[0] or 'application/octet-stream')
            else:self.send(404,b'{}')
        def do_POST(self):
            nonlocal progress
            if self.headers.get('Host') not in {f'127.0.0.1:{port}',f'localhost:{port}'} or self.path not in {'/api/notes','/api/progress','/api/reveal','/api/import','/api/process','/api/open'} or self.headers.get('X-Reader-Token')!=token:
                self.send(403,b'{}');return
            try:
                length=int(self.headers.get('Content-Length','0'))
                if self.path=='/api/import':
                    if not 0 < length <= 256*1024*1024: raise ValueError('PDF must be at most 256 MiB')
                    from urllib.parse import unquote
                    result=library.import_pdf(self.rfile.read(length),unquote(self.headers.get('X-File-Name','Imported PDF.pdf')))
                    self.send(200,json.dumps(result,ensure_ascii=False).encode());return
                if not 0<length<100000:raise ValueError('Invalid request size')
                note=json.loads(self.rfile.read(length))
                if self.path=='/api/process':
                    library.start(note['id']); self.send(202,b'{"started":true}');return
                if self.path=='/api/open':
                    main=library.read(library.LIBRARY/'main.json',{})
                    target=main['port'] if note['id']==main.get('id') else library.open_book(note['id'])
                    self.send(200,json.dumps({'port':target}).encode());return
                if self.path=='/api/reveal':
                    target=output if output.exists() else source
                    try:
                        result=subprocess.run(['gdbus','call','--session',
                            '--dest','org.freedesktop.FileManager1',
                            '--object-path','/org/freedesktop/FileManager1',
                            '--method','org.freedesktop.FileManager1.ShowItems',
                            json.dumps([target.resolve().as_uri()]),''],
                            stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=5)
                        if result.returncode:raise OSError('File manager service unavailable')
                    except (OSError,subprocess.TimeoutExpired):
                        subprocess.Popen(['xdg-open',str(target.parent)],
                            stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
                    self.send(200,b'{"opened":true}');return
                if self.path=='/api/progress':
                    if type(note['page']) is not int or note['page'] not in dimensions:raise ValueError('Invalid page')
                    for key,low,high in [('fraction',0,1),('zoom',.5,3),('saved_at',0,1e15)]:
                        if not isinstance(note[key],(float,int)) or not math.isfinite(note[key]) or not low<=note[key]<=high:raise ValueError('Invalid position')
                    if progress is None or note['saved_at']>=progress['saved_at']:
                        value={k:note[k] for k in ('page','fraction','zoom','saved_at')}
                        temporary=progress_path.with_suffix('.tmp')
                        temporary.write_text(json.dumps(value))
                        os.replace(temporary,progress_path)
                        progress=value
                    self.send(200,b'{"saved":true}');return
                if not isinstance(note['id'],str) or len(note['id'])>80:raise ValueError('Invalid ID')
                if type(note['page']) is not int or note['page'] not in dimensions:raise ValueError('Invalid page')
                if not isinstance(note['text'],str) or len(note['text'])>10000:raise ValueError('Text too long')
                for key in ('x','y','w','h'):
                    if not isinstance(note[key],(float,int)) or not 0<=note[key]<=1:raise ValueError('Invalid bounds')
                if note['w']<.01 or note['h']<.005 or not 8<=note['font_size']<=150:raise ValueError('Invalid size')
                persist({k:note[k] for k in ('id','page','text','x','y','w','h','font_size')})
                self.send(200,json.dumps(dict(saved=True,pdf=str(output))).encode())
            except (ValueError,KeyError,TypeError) as e:self.send(400,json.dumps(dict(error=str(e))).encode())
            except Exception as e:self.send(500,json.dumps(dict(error=str(e))).encode())
    HTTPServer(('127.0.0.1',port),Handler).serve_forever()

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('collection',type=Path);p.add_argument('source',type=Path)
    p.add_argument('frontend',type=Path);p.add_argument('output',type=Path);p.add_argument('state',type=Path)
    p.add_argument('--port',type=int,default=8767);a=p.parse_args()
    serve(a.collection,a.source,a.frontend,a.output,a.state,a.port)
