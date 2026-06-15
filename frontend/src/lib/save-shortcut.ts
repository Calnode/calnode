export function saveOnCmdS(save: () => void, canSave?: () => boolean) {
	return (e: KeyboardEvent) => {
		if ((e.metaKey || e.ctrlKey) && (e.key === 's' || e.key === 'S')) {
			e.preventDefault();
			if (!canSave || canSave()) save();
		}
	};
}
