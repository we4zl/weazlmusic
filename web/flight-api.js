import {state} from './state.js';
import {uuid,browserDevice} from './device.js';
export async function flightAPI(path, method='GET', body, version, signal, writer) {
  const headers=path.startsWith('portable/') ? {'X-Weazl-Device':browserDevice()} : {};
  const selected=selectedPod();
  if(selected && ['portable/status','portable/send','portable/source','portable/checkpoint','portable/devices','portable/verify'].includes(path))headers['X-Weazl-Pod']=selected;
  if(writer)headers['X-Weazl-Writer']=writer;
  if(method!=='GET'){headers['X-Weazl-Request']='1';headers['Content-Type']='application/json';headers['Idempotency-Key']=uuid();}
  if(version!==undefined)headers['If-Match']=`"v${version}"`;
  const response=await fetch('/api/v1/'+path,{method,headers,signal,body:body===undefined?undefined:JSON.stringify(body)});
  const value=response.status===204?null:await response.json();
  if(!response.ok){if(response.status===401)document.dispatchEvent(new Event('session-expired'));throw Object.assign(new Error(value?.error?.message||'Request failed.'),{code:value?.error?.code});}
  return value;
}
export async function listFlights(path){let rows=[],cursor='';do{const out=await flightAPI(path+(cursor?'?cursor='+encodeURIComponent(cursor):''));rows.push(...out.data);cursor=out.nextCursor;}while(cursor);return rows;}
export const flightIdentity=()=>[state.user,state.connection?.url||'',state.connection?.username||''].join(':');

export function selectedPod(){return sessionStorage.getItem('pod:'+flightIdentity())||'';}
export function choosePod(id){sessionStorage.setItem('pod:'+flightIdentity(),id);}
