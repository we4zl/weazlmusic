import {state, $, esc, duration, toast} from './state.js';
import {modal, closeModal} from './dialogs.js';
import {podLibraryBookmark, play, stop} from './player.js';
import {renderQueue} from './render.js';
import {saveState} from './api.js';
import {uuid} from './device.js';

const modes = ['Road Trip', 'Workout', 'Flowstate'];
const entry = item => ({entryId:item._entryId || uuid(), trackId:item.id, title:item.title,
  artist:item.artist, album:item.album, durationMs:Math.round(item.duration * 1000) || 0});

function snapshot() {
  const bookmark = podLibraryBookmark();
  if (!bookmark) throw new Error('Choose a library track first.');
  const queue = [bookmark.item, ...state.queue.filter(item => !item.url)].map(entry);
  const seconds = bookmark.seconds;
  return {version:1, kind:'we4zlp0d-trip', queue, currentEntryId:queue[0].entryId,
    positionMs:Math.max(0, Math.round((seconds || 0) * 1000)), origin:location.origin};
}

export function takeOnWe4zlP0D() {
  const panel = $('#pod-plan');
  if (!panel.hidden) { panel.querySelector('input').focus(); return; }
  const trip = snapshot();
  const opener = document.querySelector('[data-action="we4zlp0d-take"]');
  const finish = () => {
    panel.hidden = true;
    panel.replaceChildren();
    opener.setAttribute('aria-expanded','false');
    opener.focus();
  };
  const playlists = '<option value="">None</option>' + state.playlists.map(playlist =>
    `<option value="${esc(playlist.id)}">${esc(playlist.name)}</option>`).join('');
  const prefKey='pod-pack:'+JSON.stringify([state.user,state.connection?.url,state.connection?.username]);
  let prefs={};
  try {prefs=JSON.parse(localStorage.getItem(prefKey)) || {};} catch {}
  panel.innerHTML = `<h2>Export queue file</h2><p>${esc(trip.queue[0].title)} · ${duration(trip.positionMs / 1000)}</p>
    <details><summary>Queue (${trip.queue.length})</summary><ol>${trip.queue.map(item =>
      `<li>${esc(item.title || item.trackId)}</li>`).join('')}</ol></details>
    <p>Download this queue and run the Linux companion with the mounted SD card. Continue will start at the position shown above.</p>
    <form id="we4zlp0d-pack" class="auth-form">
      <label>Listening hours<input type="number" name="hours" min="0.25" max="168" step="0.25" value="8" required></label>
      <details class="pod-prefs"><summary>Listening modes, pins and favorite station</summary><div class="pod-fields">
      ${modes.map((name,index) => `<label>${name}<select name="mode${index}">${playlists}</select></label>`).join('')}
      <fieldset class="pod-pins"><legend>Pinned playlists</legend>${state.playlists.map(p=>`<label><input type="checkbox" name="pin" value="${esc(p.id)}">${esc(p.name)}</label>`).join('') || 'No playlists yet.'}</fieldset>
      <label>Flight Recorder favorite<select name="station"><option value="">None</option>${state.stations.filter(station => station.id).map(station =>
        `<option value="${esc(station.id)}">${esc(station.name)}</option>`).join('')}</select></label>
      </div></details>
      <div class="pod-form-actions"><button type="submit" class="primary">Download queue file</button>
      <button type="button" class="secondary" id="pod-cancel">Cancel</button></div>
    </form>`;
  const controls=$('#we4zlp0d-pack').elements;
  if (prefs.hours >= 0.25 && prefs.hours <= 168) controls.hours.value=prefs.hours;
  modes.forEach((name,i)=>{const select=controls.namedItem('mode'+i);select.value=prefs.modePlaylists?.[name] || '';if(select.selectedIndex<0)select.selectedIndex=0;});
  for(const input of panel.querySelectorAll('[name="pin"]'))input.checked=Array.isArray(prefs.pinPlaylistIds) && prefs.pinPlaylistIds.includes(input.value);
  controls.station.value=prefs.favoriteStationId || '';
  if(controls.station.selectedIndex<0)controls.station.selectedIndex=0;
  panel.hidden = false;
  opener.setAttribute('aria-expanded','true');
  panel.scrollIntoView({block:'start'});
  panel.querySelector('input').focus({preventScroll:true});
  $('#pod-cancel').onclick = finish;
  $('#we4zlp0d-pack').onsubmit = event => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const data = {...trip, hours:Number(form.get('hours')), modePlaylists:Object.fromEntries(
      modes.map((name,index) => [name,form.get('mode'+index)]).filter(([,id]) => id)),
      pinPlaylistIds:form.getAll('pin'), favoriteStationId:form.get('station') || null};
    const file = new Blob([JSON.stringify(data)], {type:'application/json'});
    if (file.size > 1024 * 1024) {
      toast('This trip exceeds the companion’s 1 MiB limit. Shorten the queue and try again.');
      return;
    }
    const {hours,modePlaylists,pinPlaylistIds,favoriteStationId}=data;
    try {localStorage.setItem(prefKey,JSON.stringify({hours,modePlaylists,pinPlaylistIds,favoriteStationId}));} catch {}
    const url = URL.createObjectURL(file);
    const link = document.createElement('a');
    link.href = url;
    link.download = 'we4zlp0d-trip.json';
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    finish();
  };
}

function parseReturn(text) {
  const trip = JSON.parse(text);
  if (trip.version !== 1 || trip.kind !== 'we4zlp0d-return' || trip.origin !== location.origin ||
      !Array.isArray(trip.queue) || !Number.isSafeInteger(trip.positionMs) ||
      trip.positionMs < 0 || trip.queue.some(item => !item || typeof item.entryId !== 'string' ||
        !item.entryId || typeof item.trackId !== 'string' || !item.trackId || item.trackId.length > 256 ||
        !Number.isSafeInteger(item.durationMs) || item.durationMs < 0) ||
      new Set(trip.queue.map(item => item.entryId)).size !== trip.queue.length) {
    throw new Error('Choose a valid We4zlP0D return file for this server.');
  }
  const index = trip.queue.findIndex(item => item.entryId === trip.currentEntryId);
  if (index < 0) throw new Error('The return file has no current track.');
  if (trip.queue.length - index - 1 > 1000) throw new Error('The desktop queue holds up to 1,000 upcoming tracks.');
  const track = item => ({id:item.trackId, _entryId:item.entryId, title:String(item.title || item.trackId),
    artist:String(item.artist || ''), album:String(item.album || ''), duration:item.durationMs / 1000});
  return {current:{...track(trip.queue[index]), position:trip.positionMs / 1000},
    queue:trip.queue.slice(index + 1).map(track)};
}

export function continueOnDesktop() {
  modal('<h2>Continue on desktop</h2><p>Choose the return file created by the USB companion after your trip.</p><label>We4zlP0D return file<input id="we4zlp0d-return" type="file" accept=".json,application/json"></label><div id="we4zlp0d-preview" aria-live="polite"></div>');
  $('#we4zlp0d-return').onchange = async event => {
    const input = event.currentTarget;
    const file = input.files[0];
    const preview = $('#we4zlp0d-preview');
    preview.replaceChildren();
    if (!file) return;
    try {
      if (file.size > 1024 * 1024) throw new Error('The return file is too large.');
      const session = parseReturn(await file.text());
      if (input.files[0] !== file) return;
      preview.innerHTML = `<p>${esc(session.current.title)} · ${duration(session.current.position)}</p>
        <button id="we4zlp0d-apply" class="primary">Continue playback</button>
        <button class="secondary" id="pod-return-cancel">Cancel</button>`;
      preview.querySelector('#pod-return-cancel').onclick = closeModal;
      preview.querySelector('#we4zlp0d-apply').onclick = async () => {
        if (state.remote || state.remotePending) { toast('Select this browser as the output first.'); return; }
        closeModal();
        if (state.mood) state.mood.followQueue = false;
        stop();
        state.queue = session.queue;
        await play(session.current, false);
        renderQueue();
        saveState();
      };
    } catch (error) {
      if (input.files[0] === file) preview.textContent = error.message;
    }
  };
}
