/* Plain textareas preserve native Chinese IME composition and candidate selection. */
let textMode = false, noteToken = '', notes = [];
const noteSaves = new Map();
const draftPrefix = 'chinese-book.pending.'+book.id+'.';
$('reveal').addEventListener('click', async () => {
  $('reveal').disabled = true;
  try {
    await notesReady;
    await Promise.all(notes.filter(n => noteSaves.has(n.id)).map(saveNote));
    await saveQueue;
    const response = await fetch('/api/reveal', {method:'POST',
      headers:{'Content-Type':'application/json','X-Reader-Token':noteToken},body:'{}'});
    if (!response.ok) throw new Error('Could not open the PDF location.');
  } catch (error) { $('notice').textContent = error.message; }
  finally { $('reveal').disabled = false; }
});
const notesReady = fetch('/api/notes').then(r => {
  if (!r.ok) throw new Error('Cannot load saved textboxes');
  return r.json();
}).then(data => {
  noteToken = data.token; notes = data.notes;
  notes.forEach(renderNote);
}).catch(error => { $('notice').textContent = error.message; });
async function libraryRequest(path, body, extra={}) {
  await notesReady;
  const response = await fetch(path, {method:'POST', headers:{'X-Reader-Token':noteToken,...extra},body});
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || 'Library request failed');
  return result;
}
async function openLibraryBook(id) {
  if (id === book.id) return;
  await Promise.all(notes.filter(n => noteSaves.has(n.id)).map(saveNote));
  await saveQueue; savePosition();
  const result = await libraryRequest('/api/open',JSON.stringify({id}));
  location.href = `http://127.0.0.1:${result.port}/`;
}
async function refreshLibrary() {
  const response = await fetch('/api/books');
  if (!response.ok) return;
  const data = await response.json(), select = $('bookSelect');
  const books = data.books.some(item => item.id === book.id) ? data.books : [{id:book.id,title:book.title},...data.books];
  select.replaceChildren(...books.map(item => new Option(item.title,item.id,item.id===book.id,item.id===book.id)));
  const imported = data.books.some(item => item.id === book.id && !item.port);
  $('processBook').hidden = !imported;
  if (imported) {
    const status = await fetch('/api/job?'+book.id).then(r => r.json());
    $('processBook').disabled = !['idle','error'].includes(status.state);
    $('processBook').textContent = status.state === 'error' ? 'Retry processing' : 'Process';
    $('libraryStatus').textContent = status.state === 'idle' ? '' : `${status.message || status.state}${status.total ? ` (${status.done || 0}/${status.total})` : ''}${status.state === 'error' ? ' Retry may repeat a cloud request.' : ''}`;
    if (status.state === 'running') setTimeout(refreshLibrary,2000);
    if (status.state === 'completed' && !sessionStorage.getItem('processed.'+book.id)) {
      sessionStorage.setItem('processed.'+book.id,'1'); location.reload();
    }
  }
}
async function importLibraryFile(file) {
  if (!file || !(/\.pdf$/i.test(file.name) || file.type === 'application/pdf')) throw new Error('Choose a PDF file');
  $('libraryStatus').textContent = 'Importing and preparing PDF…';
  const result = await libraryRequest('/api/import',file,{'Content-Type':'application/pdf','X-File-Name':encodeURIComponent(file.name)});
  await openLibraryBook(result.id);
  if (result.id === book.id) { $('libraryStatus').textContent = ''; await refreshLibrary(); }
}
$('bookSelect').addEventListener('change', event => openLibraryBook(event.target.value).catch(error => { $('notice').textContent=error.message; event.target.value=book.id; }));
$('addBook').onclick = () => $('pdfImport').click();
$('pdfImport').onchange = event => importLibraryFile(event.target.files[0]).catch(error => { $('libraryStatus').textContent=error.message; });
$('processBook').onclick = async () => {
  $('processBook').disabled = true;
  try { await libraryRequest('/api/process',JSON.stringify({id:book.id})); sessionStorage.removeItem('processed.'+book.id); await refreshLibrary(); }
  catch (error) { $('libraryStatus').textContent=error.message; $('processBook').disabled=false; }
};
viewer.addEventListener('dragover', event => { if (event.dataTransfer.types.includes('Files')) { event.preventDefault(); viewer.classList.add('drop-target'); } });
viewer.addEventListener('dragleave', () => viewer.classList.remove('drop-target'));
viewer.addEventListener('drop', event => { event.preventDefault(); viewer.classList.remove('drop-target'); importLibraryFile(event.dataTransfer.files[0]).catch(error => { $('libraryStatus').textContent=error.message; }); });
refreshLibrary().catch(() => {});
function setTextMode(enabled) {
  textMode = enabled;
  viewer.classList.toggle('text-mode', enabled);
}
function renderNote(note) {
  const index = book.pages.findIndex(p => p.page === note.page), sheet = sheets[index];
  if (!sheet) return;
  const field = document.createElement('textarea');
  field.className = 'note'; field.dataset.note = note.id;
  field.value = note.text; field.lang = 'zh-CN'; field.spellcheck = false;
  field.setAttribute('aria-label','PDF textbox');
  Object.assign(field.style, {left:100*note.x+'%',top:100*note.y+'%',width:100*note.w+'%',
    height:100*note.h+'%',fontSize:`calc(var(--page-width) * ${note.font_size/book.pages[index].width})`});
  // Inline text has native hit boxes for each wrapped line, unlike a textarea.
  const preview = document.createElement('div'); preview.className = 'note-preview';
  // Match the editor's language so Chromium chooses the same CJK fallback font.
  preview.lang = field.lang;
  const readout = document.createElement('span'); readout.textContent = note.text;
  preview.append(readout);
  const handle = document.createElement('div'); handle.className = 'note-resize';
  handle.title = 'Drag to change textbox width';
  for (const key of ['left','top','width','fontSize']) preview.style[key] = field.style[key];
  let composing = false, removed = false, drag = null, suppressClick = false;
  function positionHandle() {
    handle.style.left = 100*(note.x+note.w)+'%';
    handle.style.top = 100*(note.y+note.h/2)+'%';
  }
  positionHandle();
  function discard() {
    if (removed) return;
    removed = true;
    clearTimeout(noteSaves.get(note.id)); noteSaves.delete(note.id);
    note.text = '';
    notes = notes.filter(n => n.id !== note.id);
    field.remove();
    preview.remove();
    handle.remove();
    saveNote(note);
  }
  function finishEditing() {
    if (composing || removed || drag) return;
    if (!field.value.trim()) discard();
    else { update(); saveNote(note); }
  }
  function update() {
    if (removed || drag) return;
    note.text = field.value;
    readout.textContent = field.value;
    field.style.height = '1px';
    const height = Math.max(field.scrollHeight+2, parseFloat(getComputedStyle(field).fontSize)*1.5);
    note.h = Math.min(1-note.y, height/sheet.clientHeight);
    field.style.height = 100*note.h+'%';
    positionHandle();
    if (!composing) scheduleNoteSave(note);
  }
  function setWidth(width) {
    note.w = Math.max(.01, Math.min(1-note.x, width));
    field.style.width = 100*note.w+'%';
    preview.style.width = field.style.width;
    update();
  }
  field.addEventListener('compositionstart', () => { composing = true; clearTimeout(noteSaves.get(note.id)); });
  field.addEventListener('compositionend', () => {
    composing = false;
    if (document.activeElement === field) update(); else finishEditing();
  });
  field.addEventListener('input', update);
  field.addEventListener('blur', finishEditing);
  field.addEventListener('keydown', event => {
    // Enter belongs to the IME while its candidate window is active.
    if (event.isComposing || composing || event.keyCode === 229) return;
    if (event.ctrlKey && event.shiftKey && (event.key === 'ArrowLeft' || event.key === 'ArrowRight')) {
      event.preventDefault();
      setWidth(note.w + (event.key === 'ArrowRight' ? .02 : -.02));
    }
    if (event.key === 'Escape' || ((event.ctrlKey || event.metaKey) && event.key === 'Enter')) {
      event.preventDefault(); field.blur(); viewer.focus({preventScroll:true});
    }
    event.stopPropagation();
  });
  function beginPointer(event) {
    if (event.button !== 0) return;
    if (event.altKey) { event.preventDefault(); event.stopPropagation(); return; }
    if (!event.ctrlKey) return;
    event.preventDefault(); event.stopPropagation(); finishPinch();
    clearTimeout(noteSaves.get(note.id)); noteSaves.delete(note.id);
    const box = sheet.getBoundingClientRect();
    drag = {id:event.pointerId, x:event.clientX, y:event.clientY,
      left:note.x, top:note.y, width:box.width, height:box.height, moved:false};
    field.classList.add('dragging');
    field.setPointerCapture(event.pointerId);
  }
  field.addEventListener('pointerdown', beginPointer);
  preview.addEventListener('pointerdown', beginPointer);
  field.addEventListener('pointermove', event => {
    if (!drag || event.pointerId !== drag.id) return;
    event.preventDefault(); event.stopPropagation();
    const dx = event.clientX-drag.x, dy = event.clientY-drag.y;
    if (!drag.moved && Math.hypot(dx,dy)<3) return;
    drag.moved = true;
    note.x = Math.max(0, Math.min(1-note.w, drag.left+dx/drag.width));
    note.y = Math.max(0, Math.min(1-note.h, drag.top+dy/drag.height));
    field.style.left = 100*note.x+'%'; field.style.top = 100*note.y+'%';
    preview.style.left = field.style.left; preview.style.top = field.style.top;
    positionHandle();
  });
  function endDrag(event) {
    if (!drag || event.pointerId !== drag.id) return;
    const gesture = drag; drag = null;
    field.classList.remove('dragging');
    if (field.hasPointerCapture(gesture.id)) field.releasePointerCapture(gesture.id);
    if (gesture.moved) {
      suppressClick = true;
      setTimeout(() => { suppressClick = false; }, 0);
      saveNote(note);
    }
  }
  field.addEventListener('pointerup', endDrag);
  field.addEventListener('pointercancel', endDrag);
  field.addEventListener('lostpointercapture', endDrag);
  let resizing = null;
  handle.addEventListener('pointerdown', event => {
    if (event.button !== 0) return;
    event.preventDefault(); event.stopPropagation(); finishPinch();
    resizing = {id:event.pointerId, x:event.clientX, width:note.w, pageWidth:sheet.getBoundingClientRect().width};
    handle.setPointerCapture(event.pointerId);
  });
  handle.addEventListener('pointermove', event => {
    if (!resizing || event.pointerId !== resizing.id) return;
    event.preventDefault(); event.stopPropagation();
    setWidth(resizing.width+(event.clientX-resizing.x)/resizing.pageWidth);
  });
  function endResize(event) {
    if (!resizing || event.pointerId !== resizing.id) return;
    const id = resizing.id; resizing = null;
    if (handle.hasPointerCapture(id)) handle.releasePointerCapture(id);
    if (field.value.trim()) saveNote(note);
  }
  handle.addEventListener('pointerup', endResize);
  handle.addEventListener('pointercancel', endResize);
  handle.addEventListener('lostpointercapture', endResize);
  handle.addEventListener('click', event => event.stopPropagation());
  function clickNote(event) {
    event.stopPropagation();
    if (suppressClick) { suppressClick = false; event.preventDefault(); return; }
    if (removed) return;
    if (event.altKey) { event.preventDefault(); discard(); return; }
    if (event.currentTarget === preview) {
      const caret = document.caretRangeFromPoint(event.clientX,event.clientY);
      const offset = caret?.startContainer === readout.firstChild ? caret.startOffset : field.value.length;
      field.focus({preventScroll:true});
      field.setSelectionRange(offset,offset);
    }
  }
  field.addEventListener('click', clickNote);
  preview.addEventListener('click', clickNote);
  sheet.append(field,preview,handle);
  return field;
}
function scheduleNoteSave(note) {
  clearTimeout(noteSaves.get(note.id));
  noteSaves.set(note.id, setTimeout(() => saveNote(note), 700));
}
// Serialize writes, so an older request cannot replace a newer edit.
let saveQueue = Promise.resolve();
function saveNote(note) {
  clearTimeout(noteSaves.get(note.id)); noteSaves.delete(note.id);
  const snapshot = JSON.stringify(note);
  // Retain pending edits through a window close or interrupted disk write.
  try { localStorage.setItem(draftPrefix+note.id, snapshot); } catch {}
  saveQueue = saveQueue.catch(() => {}).then(async () => {
    const response = await fetch('/api/notes', {method:'POST',keepalive:true,
      headers:{'Content-Type':'application/json','X-Reader-Token':noteToken},body:snapshot});
    if (!response.ok) throw new Error('Textbox could not be saved to PDF');
    try { if (localStorage.getItem(draftPrefix+note.id) === snapshot) localStorage.removeItem(draftPrefix+note.id); } catch {}
    $('notice').textContent = '';
  }).catch(error => { $('notice').textContent = error.message+' — your draft is kept locally.'; });
  return saveQueue;
}
notesReady.then(() => {
  for (const key of Object.keys(localStorage).filter(k => k.startsWith(draftPrefix))) {
    try {
      const pending = JSON.parse(localStorage.getItem(key));
      const existing = notes.find(n => n.id === pending.id);
      if (existing) {
        Object.assign(existing,pending);
        const old = document.querySelector(`[data-note="${pending.id}"]`);
        if (old?.nextElementSibling?.classList.contains('note-preview')) {
          const preview = old.nextElementSibling;
          if (preview.nextElementSibling?.classList.contains('note-resize')) preview.nextElementSibling.remove();
          preview.remove();
        }
        old?.remove();
      }
      else notes.push(pending);
      if (pending.text.trim()) renderNote(pending);
      else notes = notes.filter(n => n.id !== pending.id);
      saveNote(pending);
    } catch {}
  }
});
document.addEventListener('keydown', event => {
  if (event.isComposing || event.repeat || event.ctrlKey || event.metaKey || event.altKey ||
      event.target.closest('input,textarea,select,[contenteditable]')) return;
  if (event.key.toLowerCase() === 't') { event.preventDefault(); dismiss(); setTextMode(!textMode); }
  if (event.key === 'Escape') setTextMode(false);
});
viewer.addEventListener('click', async event => {
  if (!textMode || event.target.closest('.note,.note-preview,.note-resize')) return;
  const sheet = event.target.closest('.sheet');
  if (!sheet) return;
  event.preventDefault(); event.stopImmediatePropagation();
  setTextMode(false);
  await notesReady;
  if (!noteToken) return;
  const index = Number(sheet.dataset.index), box = sheet.getBoundingClientRect();
  const x = Math.min(.96,Math.max(0,(event.clientX-box.left)/box.width));
  const fontSize = 36;
  const halfLineHeight = fontSize/book.pages[index].width*box.width*1.4/2;
  const y = Math.min(.96,Math.max(0,(event.clientY-box.top-halfLineHeight)/box.height));
  const note = {id:crypto.randomUUID(),page:book.pages[index].page,text:'',x,y,
    w:Math.min(.32,1-x),h:Math.min(.04,1-y),font_size:fontSize};
  notes.push(note);
  const field = renderNote(note); field.focus({preventScroll:true});
}, {capture:true});
window.addEventListener('pagehide', () => {
  for (const note of notes) if (noteSaves.has(note.id)) saveNote(note);
});
