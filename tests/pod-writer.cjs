const assert = require('node:assert/strict');
const {execFileSync} = require('node:child_process');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

const origin = process.env.TEST_URL;
const socket = process.env.TEST_DOCK_SOCKET;
const checkpointPath = '/api/v1/portable/checkpoint';
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));

function dock() {
  return JSON.parse(execFileSync('curl', ['-fsS', '--unix-socket', socket, 'http://localhost/queue'])).data;
}

async function waitForEntry(entry) {
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    if (dock().current?._entryId === entry) return;
    await sleep(150);
  }
  assert.equal(dock().current?._entryId, entry);
}

async function openPod(page) {
  await page.getByRole('navigation').getByRole('button', {name:'We4zlP0D', exact:true}).click();
  await page.getByRole('heading', {name:'We4zlP0D', exact:true}).waitFor();
}

async function playPaused(page, id, entry, next) {
  await page.evaluate(async ([id, entry, next]) => {
    const {state} = await import('/state.js');
    const {audio, play} = await import('/player.js');
    const track = {id, title:id, duration:10};
    state.queue = next ? [{...track, _entryId:next}] : [];
    await play({...track, _entryId:entry});
    audio.pause();
    await (await import('/api.js')).flushState();
  }, [id, entry, next]);
}

async function main() {
  assert.ok(origin && socket, 'Use the isolated fixture with a paired dock and set TEST_URL and TEST_DOCK_SOCKET.');
  const browser = await chromium.launch({headless:true, executablePath:process.env.BROWSER_BIN || '/usr/bin/chromium',
    args:['--autoplay-policy=no-user-gesture-required']});
  try {
    const context = await browser.newContext();
    const listening = await context.newPage();
    await listening.goto(origin);
    await listening.locator('[name=username]').fill('alice');
    await listening.locator('[name=password]').fill('test-password');
    await listening.locator('#login-form button').click();
    await listening.locator('#login-screen').waitFor({state:'hidden'});
    await openPod(listening);

    await playPaused(listening, '0-0', 'a');
    await listening.getByRole('button', {name:'Follow this browser'}).click();
    await waitForEntry('a');
    const idle = await context.newPage();
    let idleWrites = 0;
    idle.on('request', request => {if (new URL(request.url()).pathname === checkpointPath) idleWrites++;});
    await idle.goto(origin);
    await idle.locator('#login-screen').waitFor({state:'hidden'});
    await openPod(idle);
    await idle.getByRole('button', {name:'Follow this tab'}).waitFor();
    assert.equal(await idle.evaluate(async () => (await import('/state.js')).state.current.id), '0-0');

    const oldPut = listening.waitForRequest(request => new URL(request.url()).pathname === checkpointPath &&
      request.postDataJSON()?.current?._entryId === 'b-first');
    await playPaused(listening, '0-1', 'b-first', 'b-next');
    const oldWrite = (await oldPut).postDataJSON();
    await waitForEntry('b-first');
    assert.equal(dock().queue[0]._entryId, 'b-next');
    await sleep(7000);
    assert.equal(idleWrites, 0);
    assert.equal(dock().current._entryId, 'b-first');

    await idle.getByRole('button', {name:'Follow this tab'}).click();
    await waitForEntry('a');
    const delayed = await listening.evaluate(async payload => {
      try {
        await (await import('/flight-api.js')).flightAPI('portable/checkpoint','PUT',{...payload,sequence:payload.sequence+1});
        return 'accepted';
      } catch (error) { return error.code; }
    }, oldWrite);
    assert.equal(delayed, 'pod_writer_changed');
    assert.equal(dock().current._entryId, 'a');

    // Playing takes it back even when the other tab took over between polls.
    await playPaused(listening, '0-1', 'b-next');
    await waitForEntry('b-next');
    let reloadWrites = 0;
    listening.on('request', request => {if (new URL(request.url()).pathname === checkpointPath) reloadWrites++;});
    await listening.reload();
    await listening.locator('#login-screen').waitFor({state:'hidden'});
    await openPod(listening);
    await listening.getByRole('button', {name:'Follow this tab'}).waitFor();
    await sleep(5600);
    assert.equal(reloadWrites, 0);
    assert.equal(dock().current._entryId, 'b-next');

    await listening.getByText('Manage players',{exact:true}).click();
    await listening.getByRole('button', {name:'Stop following'}).click();
    await listening.getByText('Following stopped. The dock keeps the last saved bookmark.').waitFor();
    await playPaused(listening, '0-0', 'after-stop');
    assert.equal(dock().current._entryId, 'b-next');
    await context.close();
    console.log('PASS: untouched tab, explicit takeover, delayed write, duplicate track occurrence, reload, and stop');
  } finally {
    await browser.close();
  }
}

main().catch(error => { console.error(error); process.exitCode=1; });
