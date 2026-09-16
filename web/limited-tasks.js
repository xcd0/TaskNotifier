// 既存のWeb UIとPWAスケジューラに単発・回数制限を追加する。
function taskLimit(task) {
	return task.schedule.type === "once" ? 1 : (task.schedule.max_occurrences || 0);
}
function taskFinished(task) {
	const limit = taskLimit(task);
	return limit > 0 && (task.state.fired_count || 0) >= limit;
}
function countNotification(task) {
	if (taskLimit(task) === 0) return;
	task.state.fired_count = (task.state.fired_count || 0) + 1;
	if (taskFinished(task)) {
		task.state.completed = true;
		task.state.completed_at = new Date().toISOString();
	}
}
function onceOccurrence(task) {
	return {eventKey: `${task.id}:${task.schedule.date}`, scheduledAt: new Date(`${task.schedule.date}T${task.schedule.time}:00`)};
}
const originalOpenTask = O;
O = function(task) {
	originalOpenTask(task);
	i("schedule-date").value = task?.schedule.date || G(new Date());
	i("count-enabled").checked = (task?.schedule.max_occurrences || 0) > 0;
	i("max-occurrences").value = String(task?.schedule.max_occurrences || 1);
	P();
};
const originalTaskOptions = P;
P = function() {
	originalTaskOptions();
	const once = i("schedule-type").value === "once";
	const todo = i("task-kind").value === "todo";
	i("schedule-date-field").hidden = !once;
	i("schedule-date").required = once && !todo;
	i("schedule-date").disabled = !once || todo;
	if (once) i("schedule-minutes-field").hidden = true;
	for (const id of ["repeat-enabled", "period-enabled", "weekdays-enabled", "count-enabled"]) {
		const input = i(id);
		input.disabled = once || todo || (id === "period-enabled" && c.periods.length === 0);
		input.closest("label").hidden = once || todo;
	}
	i("repeat-options").hidden = once || todo || !i("repeat-enabled").checked;
	i("period-field").hidden = once || todo || !i("period-enabled").checked;
	i("weekdays-field").hidden = once || todo || !i("weekdays-enabled").checked;
	const limited = !once && !todo && i("count-enabled").checked;
	i("count-options").hidden = !limited;
	i("max-occurrences").disabled = !limited;
	i("max-occurrences").required = limited;
};
const originalReadTask = Be;
Be = function() {
	const task = originalReadTask();
	if (task.schedule.type === "once") {
		task.schedule.date = i("schedule-date").value;
		task.schedule.minutes = 0;
		task.schedule.repeat_enabled = false;
		task.schedule.end_enabled = false;
		task.schedule.end_time = "";
		task.condition = {period_enabled: false, period_id: "", weekdays_enabled: false, weekdays: []};
	} else if (i("count-enabled").checked) {
		task.schedule.max_occurrences = Number(i("max-occurrences").value);
	}
	return task;
};
const originalValidateTask = X;
X = function(task, periods) {
	originalValidateTask(task, periods);
	if (task.kind === "todo") return;
	const count = task.schedule.max_occurrences || 0;
	if (!Number.isInteger(count) || count < 0 || count > 1000000) throw new Error("実行回数は1〜1000000回で指定してください。");
	if (task.schedule.type === "once") {
		const date = task.schedule.date || "";
		const when = onceOccurrence(task).scheduledAt;
		if (!/^\d{4}-\d{2}-\d{2}$/.test(date) || !Number.isFinite(when.getTime()) || G(when) !== date) throw new Error("実行日を指定してください。");
		if (task.schedule.minutes !== 0 || task.schedule.repeat_enabled || task.schedule.end_enabled || task.condition.period_enabled || task.condition.weekdays_enabled || count > 1) throw new Error("1回だけのタスクには繰り返し・補正・期間・曜日条件を設定できません。");
	}
};
const originalFormatSchedule = Ee;
Ee = function(schedule) {
	if (schedule.type === "once") return `1回だけ ${schedule.date} ${schedule.time}`;
	return originalFormatSchedule(schedule) + (schedule.max_occurrences > 0 ? ` / 合計${schedule.max_occurrences}回` : "");
};
const originalNextOccurrence = ye;
ye = async function(task, periods, now) {
	if (taskFinished(task)) return null;
	if (task.schedule.type !== "once") return originalNextOccurrence(task, periods, now);
	const occurrence = onceOccurrence(task);
	return Z(task, occurrence.eventKey) ? null : occurrence;
};
const originalDueOccurrence = Le;
Le = async function(task, periods, now) {
	if (taskFinished(task)) return null;
	if (task.schedule.type !== "once") return originalDueOccurrence(task, periods, now);
	const occurrence = onceOccurrence(task);
	return occurrence.scheduledAt <= now && !Z(task, occurrence.eventKey) ? occurrence : null;
};
const originalBuildState = _;
_ = async function(data) {
	const state = await originalBuildState(data);
	for (const view of state.tasks) {
		if (view.task.kind === "todo") continue;
		const limit = taskLimit(view.task);
		if (limit > 0) view.condition_text += ` / ${limit}回中${view.task.state.fired_count || 0}回完了`;
		if (taskFinished(view.task)) view.next_text = "完了";
	}
	return state;
};
i("count-enabled").addEventListener("change", P);
