import {state, $, esc, duration} from './state.js';
import {flightAPI, flightIdentity} from './flight-api.js';

let status, failed, timer, controller, generation = 0, fetching = false;
const when = (value, zone) => new Date(value).toLocaleString(undefined,
  {month:'short',day:'numeric',hour:'numeric',minute:'2-digit',timeZone:zone,timeZoneName:'short'});
const fresh = report => report && Date.now() - Date.parse(report.observedAt) < 150000;
const mib = size => (size / 1048576).toLocaleString(undefined,{maximumFractionDigits:1}) + ' MiB';

function reportLine(report, radio) {
  if (!report) return '';
  const phases = {waiting:'Waiting for a mounted pod SD card',checking:'Checking the SD card',
    'no-edition':'SD card mounted · waiting for a recording',preparing:'Reading the music selection',
    music:'Verifying music',radio:'Verifying radio audio',indexing:'Writing artwork and shelves',
    failed:radio?'Radio sync needs attention':'Music pack needs attention',
    ready:radio?'Radio edition verified on card':'Music pack verified on card'};
  const transfer = ['music','radio'].includes(report.phase) && report.total > 0;
  const units = n => report.phase === 'radio' ? mib(n) : n.toLocaleString();
  const progress = transfer ? `<progress aria-label="${phases[report.phase]}" max="${report.total}" value="${report.done}"></progress><small>${units(report.done)} of ${units(report.total)}${report.phase === 'music'?' tracks':''} verified</small>` : '';
  return `<strong>${fresh(report)?'':'Last report · '}${esc(phases[report.phase] || 'Dock report')}</strong>${progress}<small>${esc(when(report.observedAt))}</small>`;
}

export function drawPodStatus() {
  const nav = $('#pod-nav-status');
  const dock = status?.dock, music = status?.music, last = music?.ready;
  const activity = [music?.latest, dock].filter(r=>fresh(r) && ['preparing','music','indexing','radio'].includes(r.phase))
    .sort((a,b)=>Date.parse(b.observedAt)-Date.parse(a.observedAt))[0];
  const navLabel = activity ? (activity.phase === 'radio' ? 'Radio sync' : 'Packing') :
    fresh(dock) && dock.phase === 'waiting' ? 'No SD mount' : '';
  nav.textContent = failed ? '' : navLabel;
  if (!$('#pod-dock')) return;
  const write = (id, html) => {const el=$(id);if(el._podHTML!==html){el.innerHTML=html;el._podHTML=html;}};
  const empty = failed ? 'Dock status unavailable. Retrying…' : status ? 'No dock report yet.' : 'Checking dock…';
  write('#pod-dock', reportLine(dock,true) || esc(empty));
  write('#pod-music-status', reportLine(music?.latest,false) || (status ? 'No music pack reported yet.' : ''));
  write('#pod-confirmed',last ? `Last verified queue · ${esc(last.title)} at ${duration(last.positionMs/1000)} · ${last.queueTracks} tracks · ${mib(last.freeBytes)} free · ${esc(when(last.observedAt))}` : '');
  const radio = status?.edition, schedule = status?.schedule, copied = status?.lastRadio;
  write('#pod-edition',radio ? `<strong>${esc(radio.name)}</strong><small>${esc(when(radio.startsAt))} · ${radio.stations} stations · available on server</small>`
    : failed ? 'Recording status unavailable.' : status ? 'No published pod recording yet.' : 'Checking recordings…');
  write('#pod-schedule',schedule ? `Next capture · ${esc(when(schedule.startsAt,schedule.zone))} – ${esc(when(schedule.endsAt,schedule.zone))} · ${schedule.stations} stations` : '');
  write('#pod-radio-ready',copied ? `Last verified on card · ${esc(copied.name)} · ${esc(when(copied.verifiedAt || copied.observedAt))}` : '');
  $('#pod-refresh-note').textContent = failed && status ? 'Showing the last report. Refresh will retry.' : '';
}

async function refresh() {
  if (!state.user || document.hidden || fetching) return;
  const gen=generation, identity=flightIdentity();
  fetching=true; controller=new AbortController();
  try {
    const response=await flightAPI('portable/status','GET',undefined,undefined,controller.signal);
    if(gen===generation && identity===flightIdentity()){status=response.data;failed=false;}
  } catch { if(gen===generation && identity===flightIdentity()) failed=true; }
  finally {if(gen===generation){fetching=false;drawPodStatus();}}
}
export function stopPodStatus() {
  generation++; controller?.abort(); clearInterval(timer); status=null; failed=false; fetching=false;
  $('#pod-nav-status').textContent='';
}
export function startPodStatus() {
  stopPodStatus(); refresh(); timer=setInterval(refresh,15000);
}
document.addEventListener('visibilitychange',()=>{if(!document.hidden)refresh();});
