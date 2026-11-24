import {state, $, esc, duration} from './state.js';
import {flightAPI, flightIdentity, selectedPod, choosePod} from './flight-api.js';
import {uuid} from './device.js';

import {podLibraryBookmark} from "./player.js";
let writer, sent, sending, changing, clock, sequence=0, checkpointFailed=false;
let status, failed, timer, controller, generation = 0, refreshing;
const when = (value, zone) => new Date(value).toLocaleString(undefined,
  {month:'short',day:'numeric',hour:'numeric',minute:'2-digit',timeZone:zone,timeZoneName:'short'});
const fresh = report => report && Date.now() - Date.parse(report.observedAt) < 150000;
const mib = size => (size / 1048576).toLocaleString(undefined,{maximumFractionDigits:1}) + ' MiB';

function reportLine(report, radio) {
  if (!report) return '';
  const phases = {ejected:'Pod safely disconnected', 'h2-control':'H2 connected · USB card transfer unavailable',waiting:'Waiting for a mounted pod SD card',checking:'Checking the SD card',
    'no-edition':'SD card mounted · waiting for a recording',preparing:'Preparing the sync',
    music:'Checking the desktop queue',shelves:'Syncing albums and discovery tracks',radio:'Verifying radio audio',indexing:'Preparing playback clips and artwork',
    failed:radio?'Radio sync needs attention':'Music pack needs attention',
    ready:radio?'Radio edition verified on card':'Queue ready on the pod'};
  const transfer = ['music','shelves','radio'].includes(report.phase) && report.total > 0;
  const units = n => report.phase === 'radio' ? mib(n) : n.toLocaleString();
  const progress = transfer ? `<progress aria-label="${phases[report.phase]}" max="${report.total}" value="${report.done}"></progress><small>${units(report.done)} of ${units(report.total)}${report.phase === 'radio'?'':' tracks'} verified</small>` : '';
  const selections = !radio && report.selections?.length ? `<small>${report.phase === 'ready' ? 'Added to the pod' : 'Selected for this sync'}</small><ul class="pod-sync-items">${report.selections.map(item => `<li>${esc(item.name)} · ${item.tracks.toLocaleString()} ${item.tracks === 1 ? 'track' : 'tracks'}</li>`).join('')}</ul>` : '';
  const active = !radio && report.active?.length ? `<small>In progress</small><ul class="pod-sync-items">${report.active.map(title => `<li>${esc(title)}</li>`).join('')}</ul>` : '';
  const title = radio ? report.name || report.title : '';
  return `<strong>${fresh(report)?'':'Last report · '}${esc(phases[report.phase] || 'Dock report')}</strong>${title ? `<small>${esc(title)}</small>` : ''}${report.message ? (report.phase==='failed' ? `<details><summary>Technical details</summary><code>${esc(report.message)}</code></details>` : `<small>${esc(report.message)}</small>`) : ''}${progress}${selections}${active}<small>${esc(when(report.observedAt))}</small>`;
}

function queueStatus(music) {
  if (status?.dock?.phase === 'ejected' && !(Date.parse(music?.latest?.observedAt) > Date.parse(status.dock.observedAt)))
    return 'Pod safely disconnected. Sync resumes on the next dock.';
  if (fresh(status?.dock) && status.dock.phase === 'h2-control') return 'The H2 is connected. This firmware supports playback controls over USB. Queue sync needs writable card access.';
  if (status?.musicMatch) return '<strong>Queue, bookmark and artwork match the player.</strong>';
  const report=music?.latest;
  const book=podLibraryBookmark();
  let matches=false;
  if (report?.queueKey && book) {
    const ids=[book.item,...state.queue.filter(t=>!t.url)].map(t=>t.id).join('\0');
    let hash=2166136261;
    for (const byte of new TextEncoder().encode(ids)) hash=Math.imul(hash^byte,16777619)>>>0;
    matches=hash.toString(16)===report.queueKey;
    if (!matches && status?.writerActive)
      return '<strong>Queue changed · waiting for the dock</strong>'+ (report.phase==='ready'?'':reportLine(report,false));
  }
  if(report?.phase==='ready') {
    return matches ? '<strong>Queue ready on the pod</strong>' :
      `<strong>Verified on the pod</strong><small>${esc(report.title || 'Music library')} · ${report.queueTracks} tracks</small>`;
  }
  if(report) return reportLine(report,false);
  if(!status) return failed ? 'Queue sync status unavailable.' : 'Checking queue sync…';
  if(status.following) return 'Waiting for the dock to pick up your queue.';
  return status.paired ? 'Choose the browser whose listening queue the pod should follow.' : 'No paired music dock for this library.';
}

export function drawPodStatus() {
  const nav = $('#pod-nav-status');
  const dock = status?.dock, music = status?.music, last = music?.ready;
  const activity = [music?.latest, dock].filter(r=>fresh(r) && ['preparing','music','shelves','indexing','radio'].includes(r.phase))
    .sort((a,b)=>Date.parse(b.observedAt)-Date.parse(a.observedAt))[0];
  const disconnected = ['waiting','ejected','h2-control'].includes(dock?.phase) && !(Date.parse(music?.latest?.observedAt) > Date.parse(dock?.observedAt));
  const radioMatches=fresh(dock) && (status?.edition
    ? dock.phase==='ready' && !!status.edition.hash && dock.hash===status.edition.hash
    : dock.phase==='no-edition');
  const synced=!!status?.musicMatch && radioMatches && !failed && !checkpointFailed &&
    !activity && !disconnected;
  nav.classList.toggle('pod-synced',synced);
  const problem = !disconnected && (status?.verification?.state==='failed' || (!status?.musicMatch && music?.latest?.phase==='failed') || dock?.phase==='failed');
  const navLabel = synced ? 'Synced' : activity && !disconnected ? (activity.phase === 'radio' ? 'Radio sync' : 'Syncing') : problem ? 'Needs attention' :
    dock?.phase === 'ejected' ? 'Disconnected' : fresh(dock) && dock.phase === 'h2-control' ? 'H2 connected' : fresh(dock) && dock.phase === 'waiting' ? 'No SD mount' : fresh(dock) ? 'Docked' : '';
  nav.textContent = failed ? '' : navLabel;
  const queuePanel=$('#list-queue-sync');
  if(queuePanel){queuePanel.hidden=!status?.paired; if(!queuePanel.hidden)queuePanel.innerHTML='<small>We4zlP0D</small>'+queueStatus(music);}
  if (!$('#pod-dock')) return;
  const write = (id, html) => {const el=$(id);if(el._podHTML!==html){el.innerHTML=html;el._podHTML=html;}};
  const selector=$('#pod-device');
  if(selector){
    write('#pod-device','<option value="">Original dock</option>'+(status?.devices||[]).map(p=>`<option value="${esc(p.id)}">${esc(p.name)}</option>`).join(''));
    selector.value=selectedPod();
    $('#pod-rename').disabled=!selectedPod();
  }
  const empty = failed ? 'Dock status unavailable. Retrying…' : status ? 'No dock report yet.' : 'Checking dock…';
  const signal=$('.pod-signal');
  signal.dataset.tone=failed || disconnected ? 'idle' : synced ? 'ready' : activity ? 'active' : problem ? 'error' : 'idle';
  write('#pod-dock',esc(failed ? 'Dock status unavailable' : synced ? 'Synced · ready to go' : activity && !disconnected ? 'Sync in progress' : navLabel || (dock ? 'Last dock report' : empty)));
  const copiedRadio=status?.lastRadio;
  const sameRadio=!!copiedRadio?.hash && copiedRadio.hash===status?.edition?.hash;
  $('.pod-stats').hidden=!last && !copiedRadio;
  write('#pod-tracks',last ? esc(last.queueTracks)+' tracks' : '—');
  write('#pod-space',last?.freeBytes != null ? (last.freeBytes/1073741824).toLocaleString(undefined,{maximumFractionDigits:1})+' GiB' : '—');
  write('#pod-stations',sameRadio ? esc(status.edition.stations)+' stations' : copiedRadio ? 'Verified' : '—');
  $('#pod-follow').hidden=!status?.paired || status.writerActive;
  $('#pod-follow').textContent=status?.following ? 'Follow this tab' : 'Follow this browser';
  $('#pod-stop-follow').hidden=!status?.writerActive;
  $('#pod-manage-stop').hidden=!status?.following || status.writerActive;
  $('#pod-listening-status').textContent = status?.following && checkpointFailed ?
    (status.writerActive ? 'Bookmark update failed. The dock still has the last saved position.' :
      'This tab could not take over. Choose Follow this tab to try again.') :
    status?.writerActive ? 'Following playback in this tab.' :
    status?.following ? 'Play here or choose Follow this tab to update the bookmark.' :
    status?.followingStopped ? 'Following stopped. The dock keeps the last saved bookmark.' : '';
  const detail = activity && !disconnected ? reportLine(activity,activity===dock) :
    !disconnected && dock?.phase==='failed' ? reportLine(dock,true) :
    status?.verification?.state==='failed' && !disconnected ? 'Card verification failed. Open Manage players to review and retry.' : queueStatus(music);
  write('#pod-music-status',detail);
  const verification=status?.verification;
  $('#pod-verify').hidden=!selectedPod() || !status?.supportsCardVerification;
  $('#pod-verify').disabled=verification?.state==='queued';
  $('#pod-verify-status').textContent=({queued:'Verification requested for the next dock check.',verified:'Card verification passed.',failed:verification?.message || 'Card verification failed.'})[verification?.state] || '';
  $('#pod-receipt').hidden=!last;
  write('#pod-confirmed',last ? ` ${esc(last.title)} at ${duration(last.positionMs/1000)} · ${last.queueTracks} tracks · ${esc(when(last.observedAt))}${last.selections?.length ? '<ul class="pod-sync-items">'+last.selections.map(item=>`<li>${esc(item.name)} · ${item.tracks} tracks</li>`).join('')+'</ul>' : ''}` : '');
  const radio = status?.edition, schedule = status?.schedule, copied = status?.lastRadio;
  write('#pod-edition',radio ? `<strong>${esc(radio.name)}</strong><small>${esc(when(radio.startsAt))} · ${radio.stations} stations</small>`
    : failed ? 'Recording status unavailable.' : status ? 'No published pod recording yet.' : 'Checking recordings…');
  write('#pod-schedule',schedule ? `Next capture · ${esc(when(schedule.startsAt,schedule.zone))} – ${esc(when(schedule.endsAt,schedule.zone))} · ${schedule.stations} stations` : '');
  write('#pod-radio-ready',copied ? `Verified on card · ${esc(copied.name)} · ${esc(when(copied.verifiedAt || copied.observedAt))}` : '');
  $('#pod-refresh-note').textContent = failed && status ? 'Showing the last report. Refresh will retry.' : '';
}

function refresh(allowHidden=false) {
  if (!state.user || (!allowHidden && document.hidden)) return;
  if (refreshing) return refreshing;
  const gen=generation, identity=flightIdentity();
  controller=new AbortController();
  refreshing=flightAPI('portable/status','GET',undefined,undefined,controller.signal,writer)
    .then(({data})=>{if(gen===generation && identity===flightIdentity()){readStatus(data);failed=false;}})
    .catch(()=>{if(gen===generation && identity===flightIdentity()) failed=true;})
    .finally(()=>{if(gen===generation){refreshing=null;drawPodStatus();}});
  return refreshing;
}
export function stopPodStatus() {
  clearInterval(clock); generation++; controller?.abort(); clearInterval(timer);
  writer=sent=sending=changing=status=refreshing=null;
  sequence=0; checkpointFailed=failed=false;
  $('#pod-nav-status').textContent='';
}
export function startPodStatus() {
  stopPodStatus(); writer=uuid();
  refresh(); timer=setInterval(refresh,5000); clock=setInterval(checkpoint,5000);
}
document.addEventListener('visibilitychange',()=>{if(!document.hidden)refresh();});

function readStatus(next, partial=false) {
  if (next.writerGeneration < status?.writerGeneration) return;
  if (next.writerGeneration !== status?.writerGeneration) {
    sent=null; sequence=0; checkpointFailed=false;
  }
  status=partial ? {...status,...next} : next;
}

async function changeWriter(action) {
  const gen=generation;
  if (changing) {
    if (action==='claim') return changing;
    await changing.catch(()=>{});
    if(gen!==generation || (action==='stop' && !status?.following)) return;
  }
  const request=flightAPI('portable/source','PUT',{action,writer,generation:status.writerGeneration});
  changing=request;
  try {
    const response=await request;
    if(gen!==generation) return;
    readStatus(response.data,true);
    drawPodStatus();
    if(response.data.writerActive) await checkpoint();
  } catch (error) {
    if(gen===generation) await refresh();
    throw error;
  } finally {
    if(changing===request) changing=null;
  }
}

export function followPodBrowser() {
  if (status?.paired) return changeWriter(status.following ? 'claim' : 'follow');
}
export function stopFollowingPod() {
  if (status?.following) return changeWriter('stop');
}
export async function podPlaybackIntent() {
  const gen=generation;
  // Another tab can take over between status polls. An explicit play action
  // uses the current generation so it can take ownership back immediately.
  if (!status || status.paired) await refresh(true);
  if (gen!==generation) return;
  if (!status?.following) return;
  if (status.writerActive) { void checkpoint(); return; }
  void changeWriter('claim').catch(() => {
    if(gen===generation) {
      checkpointFailed=true;
      drawPodStatus();
    }
  });
}

async function checkpoint() {
  if (!status?.writerActive || !state.user) return;
  const version=status.writerGeneration;
  if (sending?.version===version) return;
  const book=podLibraryBookmark();
  if(!book) return;
  const body={current:{...book.item,position:Math.floor(book.seconds)},queue:state.queue.filter(t=>!t.url)};
  const key=JSON.stringify(body);
  if(key===sent) return;
  const gen=generation;
  const request={version};
  sending=request;
  const current=()=>gen===generation && version===status?.writerGeneration && status.writerActive;
  try {
    await flightAPI('portable/checkpoint','PUT',{...body,writer,generation:version,sequence:++sequence});
    if(current()){sent=key;checkpointFailed=false;}
  } catch (error) {
    if(current()){
      checkpointFailed=true;
      if(error.code==='pod_writer_changed') {
        status={...status,writerActive:false};
        void refresh();
      }
    }
  } finally {
    if(sending===request) sending=null;
    if(gen===generation) drawPodStatus();
  }
}

document.addEventListener('change',event=>{
  if(event.target.id==='pod-device'){
    choosePod(event.target.value);
    $('#pod-pair-result').textContent='';
    startPodStatus();
  }
});
document.addEventListener('submit',async event=>{
  if(event.target.id!=='pod-device-form')return;
  event.preventDefault();
  const output=$('#pod-pair-result'), add=event.submitter?.value!=='rename';
  try{
    const {data}=await flightAPI('portable/devices',add?'POST':'PATCH',{name:event.target.elements.name.value});
    choosePod(data.device.id);
    output.textContent=add?`Run python3 companion/pair_pod.py with the mounted card path and enter this private pairing key: ${data.pairingKey}`:'Name saved. The dock will update the player.';
    startPodStatus();
  }catch(error){output.textContent=error.message;}
});

document.addEventListener('click',async event=>{
  if(event.target.id!=='pod-verify')return;
  const button=event.target, pod=selectedPod();
  button.disabled=true;
  try {await flightAPI('portable/verify','POST',{});await refresh(true);}
  catch(error){if(selectedPod()===pod && button.isConnected){$('#pod-verify-status').textContent=error.message;button.disabled=false;}}
});
