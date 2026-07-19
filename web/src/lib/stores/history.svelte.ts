// Undo/redo history for the project model. Snapshots are JSON strings of the
// whole Project; +page feeds every observed change of its `project` state in
// via observe(), and restores snapshots through the server's full-model
// replace endpoint (PUT /projects/{id}). History is per-project and in-memory
// only — switching projects resets it.

const CAP = 50;

class HistoryStore {
	#stack = $state<string[]>([]);
	#idx = $state(-1);
	#projectId = '';
	// While a restore is in flight, observed changes update the snapshot at the
	// pointer instead of recording a new entry: the server's response can differ
	// cosmetically from the stored snapshot, and recording it would truncate the
	// redo tail.
	#applying = false;

	get canUndo(): boolean {
		return this.#idx > 0;
	}
	get canRedo(): boolean {
		return this.#idx >= 0 && this.#idx < this.#stack.length - 1;
	}

	// observe ingests the current project state. A different project id resets
	// the history; an identical snapshot is a no-op.
	observe(id: string, snap: string): void {
		if (id !== this.#projectId) {
			this.#projectId = id;
			this.#stack = [snap];
			this.#idx = 0;
			return;
		}
		if (this.#applying) {
			this.#stack[this.#idx] = snap;
			return;
		}
		if (snap === this.#stack[this.#idx]) return;
		// A new edit truncates any redo tail and caps the stack.
		const kept = this.#stack.slice(Math.max(0, this.#idx + 2 - CAP), this.#idx + 1);
		this.#stack = [...kept, snap];
		this.#idx = this.#stack.length - 1;
	}

	// undo/redo move the pointer and return the snapshot to restore (null at the
	// ends). If the restore fails, the caller reverts with the opposite call.
	undo(): string | null {
		if (!this.canUndo) return null;
		this.#idx--;
		return this.#stack[this.#idx];
	}
	redo(): string | null {
		if (!this.canRedo) return null;
		this.#idx++;
		return this.#stack[this.#idx];
	}

	beginApply(): void {
		this.#applying = true;
	}
	endApply(): void {
		this.#applying = false;
	}

	clear(): void {
		this.#stack = [];
		this.#idx = -1;
		this.#projectId = '';
	}
}

export const history = new HistoryStore();
