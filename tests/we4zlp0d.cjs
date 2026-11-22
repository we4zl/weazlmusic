const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

(async () => {
  const origin = process.env.TEST_URL || 'http://127.0.0.1:14002';
  const browser = await chromium.launch({headless:true, executablePath:process.env.BROWSER_BIN ||
    '/usr/bin/chromium', args:['--autoplay-policy=no-user-gesture-required']});
  try {
    const page = await browser.newPage({viewport:{width:1440,height:900}});
    const pageErrors = [];
    page.on('pageerror', error => pageErrors.push(error.message));
    await page.goto(origin);
    await page.locator('[name=username]').fill('alice');
    await page.locator('[name=password]').fill('test-password');
    await page.getByRole('button',{name:'Sign in',exact:false}).click();
    await page.getByRole('navigation').getByRole('button',{name:'We4zlP0D',exact:true}).click();
    await page.getByRole('heading',{name:'We4zlP0D',exact:true}).waitFor();
    assert.equal(await page.locator('[data-view=we4zlp0d]').getAttribute('aria-current'),'page');
    await page.locator('.pod-return img').evaluate(img => img.decode());
    await page.evaluate(async () => {
      const {state} = await import('/state.js');
      const {play,audio} = await import('/player.js');
      const duplicate = {id:'0-0',title:'Repeated track',artist:'Fixture',duration:10};
      state.queue = [{...duplicate,_entryId:'third'},{...duplicate,id:'0-1',_entryId:'fourth'}];
      await play({...duplicate,_entryId:'second'});
      audio.currentTime = Math.min(1.2,audio.duration / 2);
    });
    await page.getByRole('button',{name:'Take on We4zlP0D'}).click();
    assert.equal(await page.locator('#modal').evaluate(el => el.open),false);
    await page.setViewportSize({width:390,height:700});
    assert.ok(await page.locator('#pod-plan').evaluate(el => el.scrollWidth <= el.clientWidth));
    await page.locator('#we4zlp0d-pack button[type=submit]').scrollIntoViewIfNeeded();
    assert.ok(await page.locator('#we4zlp0d-pack button[type=submit]').evaluate(el => {
      const r = el.getBoundingClientRect();
      return document.elementFromPoint(r.x + r.width/2,r.y + r.height/2) === el;
    }));
    // Playback can advance while the USB packing form is open.
    const atOpen = await page.evaluate(async () => (await import('/player.js')).audio.currentTime);
    await page.evaluate(async () => { (await import('/player.js')).audio.currentTime = 8; });
    const downloadEvent = page.waitForEvent('download');
    await page.getByRole('button',{name:'Download trip file'}).click();
    const download = await downloadEvent;
    const trip = JSON.parse(await fs.readFile(await download.path(),'utf8'));
    if (process.env.WE4ZL_TRIP_OUTPUT) {
      await fs.writeFile(process.env.WE4ZL_TRIP_OUTPUT,JSON.stringify(trip));
    }
    assert.equal(trip.currentEntryId,'second');
    assert.deepEqual(trip.queue.map(entry => entry.entryId),['second','third','fourth']);
    assert.ok(trip.positionMs >= 1200 && trip.positionMs <= Math.ceil(atOpen * 1000));
    assert.equal(trip.origin,origin);
    assert.equal(await page.locator('#play').getAttribute('aria-label'),'Pause');
    const returned = process.env.WE4ZL_RETURN_INPUT ? await fs.readFile(process.env.WE4ZL_RETURN_INPUT) :
      Buffer.from(JSON.stringify({...trip,kind:'we4zlp0d-return',positionMs:2400,currentEntryId:'third'}));
    async function preview() {
      await page.getByRole('button',{name:'Continue on desktop'}).click();
      await page.locator('#we4zlp0d-return').setInputFiles({name:'return.json',mimeType:'application/json',
        buffer:returned});
      await page.getByRole('button',{name:'Continue playback'}).waitFor();
    }
    await preview();
    await page.getByRole('button',{name:'Cancel',exact:true}).click();
    assert.equal(await page.evaluate(async () => (await import('/state.js')).state.current._entryId),'second');
    await preview();
    await page.getByRole('button',{name:'Continue playback'}).click();
    await page.waitForFunction(async () => (await import('/state.js')).state.current?._entryId === 'third');
    assert.deepEqual(await page.evaluate(async () =>
      (await import('/state.js')).state.queue.map(t => t._entryId)),['fourth']);
    await page.waitForFunction(async () => {
      const {audio} = await import('/player.js');
      return !audio.paused && audio.currentTime >= 2.4 && audio.currentTime < 4;
    });
    // Flight Recorder takes over the audio element while retaining the library bookmark.
    await page.evaluate(async () => {
      const {state} = await import('/state.js');
      const {stop} = await import('/player.js');
      stop();
      const station = {stationId:'fixture', name:'Fixture station'};
      state.flightPlayback = {manifest:{name:'Fixture recording', durationMs:60000, tracks:[station]},
        station:station.stationId, paused:true, position:() => 9000};
      state.resume = null;
    });
    await page.getByRole('button',{name:'Take on We4zlP0D'}).click();
    const heldDownload = page.waitForEvent('download');
    await page.getByRole('button',{name:'Download trip file'}).click();
    const held = JSON.parse(await fs.readFile(await (await heldDownload).path(),'utf8'));
    assert.equal(held.currentEntryId,'third');
    assert.ok(held.positionMs >= 2400 && held.positionMs < 4000);
    assert.deepEqual(pageErrors,[]);
    console.log('PASS: click-time export, repeated track, cancel, returned seek and held library bookmark');
  } finally {
    await browser.close();
  }
})().catch(error => {console.error(error);process.exitCode = 1;});
