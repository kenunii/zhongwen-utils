/* Continuous, offline PDF reading with prepared word translations. */
const $ = id => document.getElementById(id);
const viewer = $('pages');
const storageKey = 'chinese-book.page.' + book.id;
const positionKey = 'chinese-book.position.' + book.id;
let current = 0, zoom = 1, selected = null, saveTimer, scrollFrame;
let pinch = null, pinchTimer, pinchFrame;
let progressToken = '', progressReady = false, positionTouched = false;
const sheets = [];
const unloadTimers = new Map();
const nearbyPages = new Set(), visiblePages = new Set();
$('bookSelect').add(new Option(book.title, book.id, true, true));
function dismiss() {
  $('popup').hidden = true;
  document.querySelectorAll('.selected').forEach(el => el.classList.remove('selected'));
  selected = null;
}
function positionPopup(target) {
  const box = target.getBoundingClientRect(), pop = $('popup');
  const left = Math.max(12, Math.min(box.left, innerWidth - pop.offsetWidth - 12));
  const below = box.bottom + 10;
  const top = below + pop.offsetHeight < innerHeight - 12 ? below : box.top - pop.offsetHeight - 10;
  pop.style.left = left + 'px';
  pop.style.top = Math.max(62, Math.min(top, innerHeight - pop.offsetHeight - 12)) + 'px';
}
function lookup(pageIndex, wordIndex, target) {
  dismiss();
  selected = target;
  const word = book.pages[pageIndex].data.words[wordIndex];
  const sentence = book.pages[pageIndex].data.sentences[word.sentence];
  $('hanzi').textContent = word.hanzi;
  $('pinyin').textContent = word.pinyin;
  $('meaning').textContent = word.meaning;
  $('source').textContent = sentence?.text || '';
  $('sentence').textContent = sentence?.translation || '';
  $('sentenceDetails').hidden = !sentence?.translation;
  sheets[pageIndex].querySelectorAll(`[data-word="${wordIndex}"]`).forEach(el => el.classList.add('selected'));
  $('popup').hidden = false;
  positionPopup(target);
}
function mount(index) {
  const sheet = sheets[index];
  clearTimeout(unloadTimers.get(index));
  unloadTimers.delete(index);
  if (sheet.dataset.loaded) return;
  sheet.dataset.loaded = 'true';
  const p = book.pages[index];
  sheet.querySelector('img').src = p.image;
  const regions = sheet.querySelector('.regions');
  const fragment = document.createDocumentFragment();
  (p.data?.words || []).forEach((word, i) => {
    if (!word.pinyin && !word.meaning) return;
    word.boxes.forEach(([x, y, w, h]) => {
      const el = document.createElement('button');
      el.className = 'word'; el.dataset.word = i;
      el.setAttribute('aria-label', word.hanzi);
      Object.assign(el.style, {left:100*x/p.width+'%', top:100*y/p.height+'%', width:100*w/p.width+'%', height:100*h/p.height+'%'});
      fragment.append(el);
    });
  });
  regions.append(fragment);
}
function unmount(index) {
  const sheet = sheets[index];
  if (!sheet.dataset.loaded) return;
  if (selected && sheet.contains(selected)) dismiss();
  sheet.querySelector('img').removeAttribute('src');
  sheet.querySelector('.regions').replaceChildren();
  delete sheet.dataset.loaded;
}
// Delegate word interactions so page changes do not create hundreds of handlers.
viewer.addEventListener('click', event => {
  const word = event.target.closest('.word');
  if (!word || !viewer.contains(word)) return;
  event.stopPropagation();
  lookup(Number(word.closest('.sheet').dataset.index), Number(word.dataset.word), word);
});
function highlightWord(word, active) {
  if (!word) return;
  word.parentElement.querySelectorAll(`[data-word="${word.dataset.word}"]`)
    .forEach(part => part.classList.toggle('hovered', active));
}
viewer.addEventListener('pointerover', event => {
  if (event.pointerType !== 'touch') highlightWord(event.target.closest('.word'), true);
});
viewer.addEventListener('pointerout', event => highlightWord(event.target.closest('.word'), false));
function needed(index) {
  return nearbyPages.has(index) || [...visiblePages].some(visible => Math.abs(visible-index) <= 1);
}
function updatePage(index) {
  if (index < 0 || index >= sheets.length) return;
  if (needed(index)) mount(index);
  else if (sheets[index].dataset.loaded && !unloadTimers.has(index)) {
    // Avoid rebuilding a page when scrolling back and forth near a boundary.
    unloadTimers.set(index, setTimeout(() => {
      unloadTimers.delete(index);
      if (!needed(index)) unmount(index);
    }, 3000));
  }
}
// Keep the visible pages and their immediate neighbors loaded even at high zoom.
const visibleObserver = new IntersectionObserver(entries => {
  if (pinch) return;
  const changed = new Set();
  for (const entry of entries) {
    const index = Number(entry.target.dataset.index);
    if (entry.isIntersecting) visiblePages.add(index); else visiblePages.delete(index);
    for (let i = index-1; i <= index+1; i++) changed.add(i);
  }
  for (const index of changed) updatePage(index);
}, {root:viewer});
// Preload additional pages within the scroll margin.
const observer = new IntersectionObserver(entries => {
  if (pinch) return;
  for (const entry of entries) {
    const index = Number(entry.target.dataset.index);
    if (entry.isIntersecting) nearbyPages.add(index); else nearbyPages.delete(index);
    updatePage(index);
  }
}, {root:viewer, rootMargin:'1600px 0px'});
book.pages.forEach((p, i) => {
  const sheet = document.createElement('section');
  sheet.className = 'sheet'; sheet.dataset.index = i;
  sheet.setAttribute('aria-label', `PDF page ${p.page}`);
  sheet.style.aspectRatio = `${p.width} / ${p.height}`;
  const image = document.createElement('img');
  image.className = 'scan'; image.width = p.width; image.height = p.height;
  image.decoding = 'async';
  image.alt = `PDF page ${p.page}`; image.draggable = false;
  image.onerror = () => { if (image.hasAttribute('src')) $('notice').textContent = `Could not load page ${p.page}.`; };
  const regions = document.createElement('div'); regions.className = 'regions';
  sheet.append(image, regions); $('pages').append(sheet); sheets.push(sheet);
  observer.observe(sheet);
  visibleObserver.observe(sheet);
});
function position() {
  const y = viewer.scrollTop + 12;
  let low = 0, high = sheets.length - 1;
  while (low < high) {
    const mid = Math.ceil((low + high) / 2);
    if (sheets[mid].offsetTop <= y) low = mid; else high = mid - 1;
  }
  return {page:book.pages[low].page, fraction:Math.max(0, Math.min(1, (y-sheets[low].offsetTop)/sheets[low].offsetHeight)), zoom};
}
function restore(pos) {
  const index = Math.max(0, book.pages.findIndex(p => p.page === pos.page));
  mount(index);
  viewer.scrollTo({top:Math.max(0, sheets[index].offsetTop + (pos.fraction || 0)*sheets[index].offsetHeight-12), behavior:'instant'});
  current = index;
  $('page').value = book.pages[current].page;
}
function savePosition() {
  if (!progressReady && !positionTouched) return;
  const pos = {...position(), saved_at:Date.now()};
  try {
    localStorage.setItem(positionKey, JSON.stringify(pos));
    localStorage.setItem(storageKey, String(pos.page));
  } catch {}
  if (progressToken) fetch('/api/progress', {
    method:'POST', keepalive:true,
    headers:{'Content-Type':'application/json','X-Reader-Token':progressToken},
    body:JSON.stringify(pos),
  }).catch(() => {}); // Browser storage remains available if the server is restarting.
}
function layout() {
  document.documentElement.style.setProperty('--page-width', Math.max(240, Math.min(viewer.clientWidth-24, 1000)*zoom)+'px');
  $('zoom').textContent = zoom === 1 ? 'Fit width' : Math.round(zoom*100)+'%';
}
function changeZoom(factor) {
  finishPinch();
  const pos = position();
  zoom = Math.max(.5, Math.min(3, factor === null ? 1 : zoom*factor));
  dismiss(); layout(); restore(pos); savePosition();
}
function zoomAt(factor, clientX, clientY) {
  const index = Math.max(0, sheets.findIndex(sheet => {
    const box = sheet.getBoundingClientRect();
    return box.bottom >= clientY;
  }));
  const sheet = sheets[index], before = sheet.getBoundingClientRect();
  const x = (clientX-before.left)/before.width;
  const y = (clientY-before.top)/before.height;
  zoom = Math.max(.5, Math.min(3, zoom*factor));
  dismiss(); layout();
  const after = sheet.getBoundingClientRect();
  viewer.scrollBy({left:after.left+x*after.width-clientX,
    top:after.top+y*after.height-clientY, behavior:'instant'});
  savePosition();
}
function previewPinch(event) {
  if (!pinch) {
    dismiss();
    const layers = sheets.filter(sheet => sheet.dataset.loaded).map(sheet => {
      const box = sheet.getBoundingClientRect();
      sheet.style.transformOrigin = `${event.clientX-box.left}px ${event.clientY-box.top}px`;
      sheet.style.willChange = 'transform';
      return sheet;
    });
    pinch = {factor:1, x:event.clientX, y:event.clientY, layers};
  }
  pinch.factor = Math.max(.5/zoom, Math.min(3/zoom,
    pinch.factor * Math.exp(-Math.max(-30, Math.min(30, event.deltaY))*.01)));
  if (!pinchFrame) pinchFrame = requestAnimationFrame(() => {
    pinchFrame = null;
    if (pinch) for (const layer of pinch.layers) layer.style.transform = `scale(${pinch.factor})`;
  });
  clearTimeout(pinchTimer);
  pinchTimer = setTimeout(finishPinch, 160);
}
function finishPinch() {
  if (!pinch) return;
  clearTimeout(pinchTimer);
  cancelAnimationFrame(pinchFrame); pinchFrame = null;
  const gesture = pinch;
  for (const layer of gesture.layers) {
    layer.style.transform = '';
    layer.style.transformOrigin = '';
    layer.style.willChange = '';
  }
  pinch = null;
  zoomAt(gesture.factor, gesture.x, gesture.y);
  // Refresh visibility once, after the final page dimensions are committed.
  for (const sheet of sheets) {
    observer.unobserve(sheet); observer.observe(sheet);
    visibleObserver.unobserve(sheet); visibleObserver.observe(sheet);
  }
}
$('total').textContent = '/ ' + book.pages.at(-1).page;
$('page').max = book.pages.at(-1).page;
$('page').min = book.pages[0].page;
$('page').onchange = () => {
  positionTouched = true;
  const n = Number($('page').value);
  if (book.pages.some(p => p.page === n)) { dismiss(); restore({page:n}); savePosition(); }
  else $('page').value = book.pages[current].page;
};
$('in').onclick = () => changeZoom(1.2);
$('out').onclick = () => changeZoom(1/1.2);
$('zoom').onclick = () => changeZoom(null);
$('close').onclick = dismiss;
$('sentenceDetails').ontoggle = () => { if (selected) positionPopup(selected); };
document.addEventListener('click', e => { if (!$('popup').contains(e.target)) dismiss(); });
document.addEventListener('keydown', e => {
  if (e.isComposing || e.keyCode === 229) return;
  if (['ArrowDown','ArrowUp','PageDown','PageUp','Home','End',' ','+','=','-','0'].includes(e.key)) positionTouched = true;
  if (e.key === 'Escape') { dismiss(); e.target.blur(); }
  if (e.target.matches('input,textarea,select')) return;
  if (e.key === '+' || e.key === '=') { e.preventDefault(); changeZoom(1.2); }
  if (e.key === '-') { e.preventDefault(); changeZoom(1/1.2); }
  if (e.key === '0') { e.preventDefault(); changeZoom(null); }
  // Arrows, Page Up/Down and Space retain native browser scrolling.
});
// Intercept pinch only; ordinary scrolling stays with the browser.
viewer.addEventListener('wheel', event => {
  positionTouched = true;
  // Chromium reports trackpad pinch gestures as Ctrl+wheel.
  if (event.ctrlKey && event.cancelable) {
    event.preventDefault();
    previewPinch(event);
    return;
  }
  finishPinch();
}, {passive:false});
viewer.addEventListener('pointerdown', () => { positionTouched = true; }, {passive:true});
viewer.addEventListener('scroll', () => {
  if (pinch) return;
  if (!$('popup').hidden) dismiss();
  if (!scrollFrame) scrollFrame = requestAnimationFrame(() => {
    scrollFrame = null;
    const pos = position();
    current = Math.max(0, book.pages.findIndex(p => p.page === pos.page));
    if (document.activeElement !== $('page')) $('page').value = pos.page;
    clearTimeout(saveTimer); saveTimer = setTimeout(savePosition, 200);
  });
}, {passive:true});
window.addEventListener('resize', () => { finishPinch(); const pos = position(); dismiss(); layout(); restore(pos); });
window.addEventListener('pagehide', () => { finishPinch(); savePosition(); });
window.addEventListener('beforeunload', () => { finishPinch(); savePosition(); });
document.addEventListener('visibilitychange', () => { if (document.hidden) savePosition(); });
history.scrollRestoration = 'manual';
let initial = {page:book.pages[0].page};
try {
  initial = JSON.parse(localStorage.getItem(positionKey)) || {page:Number(localStorage.getItem(storageKey)) || book.pages[0].page};
} catch {}
zoom = Math.max(.5, Math.min(3, Number(initial.zoom) || 1));
layout(); restore(initial);
viewer.focus({preventScroll:true});
// Keep a disk-backed copy as well as browser storage, including on window close.
const progressLoaded = fetch('/api/progress').then(response => {
  if (!response.ok) throw new Error('Position storage unavailable');
  return response.json();
}).then(data => {
  progressToken = data.token;
  if (!positionTouched && data.position &&
      (data.position.saved_at || 0) > (initial.saved_at || 0)) {
    zoom = data.position.zoom;
    layout(); restore(data.position);
  }
}).catch(() => {}).finally(() => {
  progressReady = true;
  savePosition();
});
