export const state = {
  managed: [],
  selectedID: "",
  activeModal: null, // 'config', 'manifest', 'files', 'probe', 'audit', 'generator'
};

export function getSelectedService() {
  return state.managed.find((item) => item.service_id === state.selectedID) || null;
}
