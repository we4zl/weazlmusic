import {remoteControl, remoteLoad} from './remote.js';
import {state, $, toast, duration} from './state.js';
import {saveState} from './api.js';
import {renderPlayer, renderQueue, updatePodSession} from './render.js';
import {closeRadioMetadata, watchRadioMetadata} from './radio-metadata.js';
import {stopFlightPlayback,toggleFlight,seekFlight} from './flight-playback.js';
import {queueEntry} from './device.js';
import {podPlaybackIntent} from './pod-status.js';
export const audio = new Audio();
audio.preload = 'none';
audio.volume = 0.7;

export function podLibraryBookmark() {
  const item = state.current?.url ? state.resume : state.current;
  if (!item || item.url || (state.remote && !state.remote.state.playback)) return null;
  const live = !state.flightPlayback && item === state.current && audio.readyState >= 1 && audio.getAttribute('src');
  const seconds = state.remote && item === state.current ? state.remote.state.positionMs / 1000 :
    live ? audio.currentTime : item.position;
  return {item, seconds:Math.max(0, Number(seconds) || 0)};
}
let previousTracks = [], playVersion = 0;
function rememberPosition() {
  if (!state.flightPlayback && state.current && !state.current.url && audio.getAttribute('src') && audio.readyState >= 1 && Number.isFinite(audio.currentTime)) state.current.position = audio.currentTime;
}
export function stop() {
  if(state.remote){remoteControl("stop");return;}
  state.transferVersion=(state.transferVersion||0)+1;
  rememberPosition();
  stopFlightPlayback();
  closeRadioMetadata();
  playVersion++; audio.pause(); audio.removeAttribute('src'); audio.load(); state.playing = false;
}
export function resetPlayer() { state.remote=null;state.localOutputSaved=null;stop(); previousTracks = []; state.current = null; state.resume = null; renderPlayer(); }
export async function play(t, remember = true, explicit = true) {
  if (!t) return;
  if(state.remote){
    if(await remoteLoad(t,state.queue) && explicit) podPlaybackIntent();
    return;
  }
  state.transferVersion=(state.transferVersion||0)+1;
 rememberPosition();
 stopFlightPlayback();
  closeRadioMetadata();
  const version = ++playVersion;
  if (remember && state.current && !state.current.url) previousTracks.push(state.current);
  if (t.url && state.current && !state.current.url) state.resume = {...state.current};
  audio.pause(); audio.removeAttribute('src'); audio.load();
  state.current = t._entryId ? t : queueEntry(t); state.playing = false;
  const position = Number(t.position) || 0;
  audio.onloadedmetadata = () => {if (version === playVersion && !t.url && position > 0 && Number.isFinite(audio.duration)) audio.currentTime = Math.min(position,audio.duration);};
  const playback = t.url ? watchRadioMetadata(t) : '';
  audio.src = t.url ? '/api/radio/stream?' + new URLSearchParams({url:t.url,playback}) : '/api/media/stream?' + new URLSearchParams({id:t.id});
  $('#seek').value = 0; $('#elapsed').textContent = '0:00';
  renderPlayer(); saveState();
  if(explicit) podPlaybackIntent();
  try { await audio.play(); }
  catch (error) { if (version === playVersion && error.name !== 'AbortError') {closeRadioMetadata();toast('Playback could not start. Check the stream or browser audio format support.');} }
}
export async function toggle() {
 if(state.remotePending)state.transferVersion=(state.transferVersion||0)+1;
 if(state.remote){const starting=!state.playing;if(await remoteControl(starting?"play":"pause") && starting)podPlaybackIntent();return;}
 if(state.flightPlayback){await toggleFlight();return;}
  if (!state.current) { if (state.queue.length) next(); else toast('Pick a track or station first.'); return; }
  if (state.playing) { rememberPosition(); if (state.current.url) stop(); else audio.pause(); saveState(); }
  else if (!audio.getAttribute('src')) await play(state.current,false);
  else { try { await audio.play(); podPlaybackIntent(); } catch { toast('Playback could not start.'); } }
  renderPlayer();
}
export function next(explicit = true) { if(state.remote){void remoteControl("next").then(applied=>{if(applied)podPlaybackIntent();});return;} if (state.queue.length) { play(state.queue.shift(),true,explicit); renderQueue(); saveState(); } }
export function previous() { if(state.remote){void remoteControl("previous").then(applied=>{if(applied)podPlaybackIntent();});return;} const t = previousTracks.pop(); if (t) play(t,false); }
export function playList(items) {
  if (!items.length) return;
  const [current,...queue] = items.map(queueEntry);
  if (state.remote) return remoteLoad(current,queue)?.then(applied=>{if(applied)podPlaybackIntent();});
  state.queue = queue; play(current); renderQueue(); saveState();
}
export function enqueue(t) { state.queue.push(queueEntry(t)); renderQueue(); renderPlayer(); saveState(); toast('Added to your queue.'); }
audio.addEventListener('play',() => {if(state.remote)return;state.playing = true; renderPlayer();});
audio.addEventListener('pause',() => {if(state.remote)return;state.playing = false;renderPlayer();});
audio.addEventListener('ended',() => {if(state.remote||state.flightPlayback)return;closeRadioMetadata();state.playing = false; next(false); renderPlayer();});
audio.addEventListener('error',() => {if (!state.remote && audio.getAttribute('src')) {closeRadioMetadata();state.playing = false;renderPlayer();toast('Audio unavailable. The source may be offline or use an unsupported format.');}});
audio.addEventListener('timeupdate',() => {
  if (state.remote || document.hidden || state.current?.url || state.flightPlayback) return;
  updatePodSession();
  $('#elapsed').textContent = duration(audio.currentTime);
  $('#seek').value = Number.isFinite(audio.duration) && audio.duration > 0 ? audio.currentTime / audio.duration * 100 : 0;
});
$('#seek').addEventListener('input',event => {if(state.remote){remoteControl('seek',{positionMs:Math.round((state.remote.state.durationMs||0)*Number(event.target.value)/100)});return;}if(state.flightPlayback){seekFlight(state.flightPlayback.manifest.durationMs*Number(event.target.value)/100);return;}if (Number.isFinite(audio.duration) && !state.current?.url) audio.currentTime = audio.duration * Number(event.target.value) / 100;});
$('#volume').addEventListener('input',event => {if(state.remote){remoteControl('setVolume',{volume:Number(event.target.value)/100});return;}audio.volume = Number(event.target.value) / 100;});
if ('mediaSession' in navigator) {
  navigator.mediaSession.setActionHandler('play',toggle);
  navigator.mediaSession.setActionHandler('pause',() => {if (state.playing) toggle();});
  navigator.mediaSession.setActionHandler('nexttrack',()=>next());
  navigator.mediaSession.setActionHandler('previoustrack',previous);
}
