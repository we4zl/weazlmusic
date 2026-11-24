const assert = require('node:assert/strict');
const {createHash} = require('node:crypto');
const {execFileSync} = require('node:child_process');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

(async () => {
  const browser = await chromium.launch({executablePath:'/usr/bin/chromium',headless:true});
  try {
    const page = await browser.newPage();
    await page.goto(process.env.TEST_URL);
    await page.locator('[name=username]').fill('alice');
    await page.locator('[name=password]').fill('test-password');
    await page.locator('#login-form button').click();
    await page.locator('#login-screen').waitFor({state:'hidden'});
    const token = await page.evaluate(async () => {
      const {choosePod,flightAPI} = await import('/flight-api.js');
      const {data} = await flightAPI('portable/devices','POST',{name:'Receipt fixture'});
      choosePod(data.device.id);
      (await import('/pod-status.js')).startPodStatus();
      return data.pairingKey;
    });
    await page.getByRole('navigation').getByRole('button',{name:'We4zlP0D',exact:true}).click();
    const dock = (path,body) => {
      const send = body ? ['-X','PUT','-H','Content-Type: application/json','-d',JSON.stringify(body)] : [];
      const args=['-fsS','--unix-socket',process.env.TEST_DOCK_SOCKET,'-H','X-Weazl-Pod-Key: '+token,...send,'http://localhost'+path];
      const raw=execFileSync('curl',args,{encoding:'utf8'});
      return raw && JSON.parse(raw).data;
    };
    const hash = rev => createHash('sha256').update(Buffer.from(rev,'hex')).digest('hex');
    const art=dock('/recent?receipts=1');
    const first=dock('/queue?receipts=1');
    const proof='&music='+hash(first.musicRevision)+'&art='+hash(art.revision)+'&bookmark='+first.bookmarkRevision;
    const match=dock('/queue?receipts=1'+proof);
    assert.equal(match.musicMatch,true);
    assert.equal(match.queue,undefined);
    dock('/radio-status',{phase:'no-edition'});
    const label=page.locator('#pod-nav-status');
    const refresh=()=>page.evaluate(async()=>{(await import('/pod-status.js')).startPodStatus()});
    await refresh();
    await page.waitForFunction(()=>document.querySelector('#pod-nav-status').textContent==='Synced');
    assert.equal(await label.evaluate(el=>getComputedStyle(el).color),'rgb(128, 223, 176)');
    await page.evaluate(async () => (await import('/flight-api.js')).flightAPI(
      'portable/status','PUT',{phase:'failed',message:'USB receipt read failed'}));
    await refresh();
    await page.waitForFunction(()=>document.querySelector('#pod-nav-status').textContent==='Needs attention');
    assert.equal(await page.locator('#pod-music-status code').isVisible(),false);
    dock('/queue?receipts=1'+proof);
    await refresh();
    await page.waitForFunction(()=>document.querySelector('#pod-nav-status').textContent==='Synced');
    assert.ok(!(await page.locator('#pod-music-status').textContent()).includes('failed'));
    await page.getByText('Manage players',{exact:true}).click();
    await page.getByRole('button',{name:'Verify card',exact:true}).click();
    await page.getByText('Verification requested for the next dock check.').waitFor();
    assert.notEqual(await label.textContent(),'Synced');
    const {verifyRequest}=dock('/queue?receipts=1'+proof);
    assert.ok(verifyRequest);
    dock('/verify-result',{id:verifyRequest,error:'Verify card failed: fixture.m4a'});
    await refresh();
    await page.getByText('Verify card failed: fixture.m4a').waitFor();
    assert.notEqual(await label.textContent(),'Synced');
    await page.getByRole('button',{name:'Verify card',exact:true}).click();
    await page.getByText('Verification requested for the next dock check.').waitFor();
    dock('/verify-result',{id:dock('/queue?receipts=1'+proof).verifyRequest});
    await refresh();
    await page.waitForFunction(()=>document.querySelector('#pod-nav-status').textContent==='Synced');
    dock('/radio-status',{phase:'ejected'});
    await refresh();
    await page.waitForFunction(()=>document.querySelector('#pod-nav-status').textContent==='Disconnected');
    assert.ok(!await label.evaluate(el=>el.classList.contains('pod-synced')));
    await page.evaluate(async () => (await import('/flight-api.js')).flightAPI(
      'portable/send','POST',{kind:'album',id:'reissue',name:'Reissue',tracks:['one','two']}));
    const changed=dock('/queue?receipts=1'+proof);
    assert.equal(changed.musicMatch,false);
    assert.notEqual(changed.musicRevision,first.musicRevision);
    execFileSync('curl',['-fsS','--unix-socket',process.env.TEST_DOCK_SOCKET,'-H','X-Weazl-Pod-Key: '+token,
      '-H','Content-Type: application/json','-d',JSON.stringify([changed.sends[0].receipt]),'http://localhost/delivered']);
    const oldCard=dock('/queue?receipts=1'+proof);
    assert.equal(oldCard.musicMatch,false);
    assert.equal(oldCard.musicRevision,changed.musicRevision);
    assert.deepEqual(oldCard.selections[0].tracks,['one','two']);
    console.log('PASS: named-card digest, compact response, green Synced, audit failure/retry and safe disconnect and restoration of delivered albums');
  } finally {await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1});
