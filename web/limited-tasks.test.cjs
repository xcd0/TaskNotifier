const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const zlib = require('node:zlib');
const path = require('node:path');

async function main() {
	const root = path.join(__dirname, '..');
	const encoded = [0, 1, 2].map(n => fs.readFileSync(path.join(root, `ci-generated/webview.part0${n}`), 'utf8').trim()).join('');
	const html = zlib.gunzipSync(Buffer.from(encoded, 'base64')).toString();
	const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
	const helper = fs.readFileSync(path.join(__dirname, 'limited-tasks.js'), 'utf8');
	assert.ok(script.includes(helper), 'generated UI must contain the current helper source');
	const context = vm.createContext({window: {addEventListener() {}}, document: {getElementById: () => ({addEventListener() {}})}, console, Date, setTimeout, structuredClone});
	vm.runInContext(script.replace('qe();})();', 'globalThis.testAPI={Ce,X,Le,ye,countNotification,taskFinished,Ee};})();'), context);
	const api = context.testAPI;
	const once = api.Ce();
	once.id = 'single'; once.title = '単発'; once.schedule.type = 'once'; once.schedule.date = '2026-10-01';
	api.X(once, []);
	assert.equal(await api.Le(once, [], new Date('2026-09-01T09:00:00')), null);
	assert.ok(await api.ye(once, [], new Date('2026-09-01T09:00:00')));
	assert.ok(await api.Le(once, [], new Date('2026-11-01T09:00:00')));
	api.countNotification(once);
	assert.equal(once.state.fired_count, 1);
	assert.equal(await api.ye(once, [], new Date('2026-11-01T09:00:00')), null);
	assert.equal(await api.Le(once, [], new Date('2026-11-01T09:00:00')), null);
	const count = api.Ce(); count.id = 'limited'; count.title = '回数'; count.schedule.max_occurrences = 2;
	for (let day = 16; day <= 17; day++) {
		const event = await api.Le(count, [], new Date(`2026-09-${day}T09:00:00`));
		assert.ok(event);
		api.countNotification(count); count.state.last_fired_event = event.eventKey;
	}
	assert.ok(api.taskFinished(count));
	assert.equal(await api.Le(count, [], new Date('2026-09-18T09:00:00')), null);
	assert.equal(api.Ee(count.schedule).includes('合計2回'), true);
	const invalid = structuredClone(once); invalid.schedule.date = '2026-02-30';
	assert.throws(() => api.X(invalid, []));
	const unlimited = api.Ce(); api.countNotification(unlimited);
	assert.equal(api.taskFinished(unlimited), false);
	console.log('PWA: future/overdue single task, count limit, validation, generated-source consistency passed.');
}
main().catch(error => { console.error(error); process.exitCode = 1; });
