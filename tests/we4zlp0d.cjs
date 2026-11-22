const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

(async () => {
  const origin = process.env.TEST_URL || 'http://127.0.0.1:14002';
  const browser = await chromium.launch({headless:true, executablePath:process.env.BROWSER_BIN ||
    '/usr/bin/chromium', args:['--autoplay-policy=no-user-gesture-required']});
  try {
    const page = await browser.newPage({viewport:{width:1440,height:900}});
    await page.goto(origin);
    await page.locator('[name=username]').fill('alice');
    await page.locator('[name=password]').fill('test-password');
    await page.getByRole('button',{name:'Sign in',exact:false}).click();
    await page.getByRole('button',{name:'Take on We4zlP0D'}).waitFor();
    await page.evaluate(async () => {
      const {state} = await import('/state.js');
      const {play,audio} = await import('/player.js');
      const duplicate = {id:'0-0',title:'Repeated track',artist:'Fixture',duration:10};
      state.queue = [{...duplicate,_entryId:'third'},{...duplicate,id:'0-1',_entryId:'fourth'}];
      await play({...duplicate,_entryId:'second'});
      audio.currentTime = Math.min(1.2,audio.duration / 2);
    });
    await page.getByRole('button',{name:'Take on We4zlP0D'}).click();
    const downloadEvent = page.waitForEvent('download');
    await page.getByRole('button',{name:'Download trip file'}).click();
    const download = await downloadEvent;
    const trip = JSON.parse(await fs.readFile(await download.path(),'utf8'));
    if (process.env.WE4ZL_TRIP_OUTPUT) {
      await fs.writeFile(process.env.WE4ZL_TRIP_OUTPUT,JSON.stringify(trip));
    }
    assert.equal(trip.currentEntryId,'second');
    assert.deepEqual(trip.queue.map(entry => entry.entryId),['second','third','fourth']);
    assert.ok(trip.positionMs > 0);
    assert.equal(await page.locator('#play').getAttribute('aria-label'),'Pause');
    const returned = {...trip,kind:'we4zlp0d-return',positionMs:70,currentEntryId:'third'};
    async function preview() {
      await page.getByRole('button',{name:'Continue on desktop'}).click();
      await page.locator('#we4zlp0d-return').setInputFiles({name:'return.json',mimeType:'application/json',
        buffer:Buffer.from(JSON.stringify(returned))});
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
    console.log('PASS: live export, duplicate occurrence, cancel, and return playback');
  } finally {
    await browser.close();
  }
})().catch(error => {console.error(error);process.exitCode = 1;});
