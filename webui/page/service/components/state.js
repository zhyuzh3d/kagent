export const state = {
  managed: [],
  registered: [],
  tools: [],
  selectedID: "",
  activeModal: "lifecycle",
  activeMainTab: "services",
  appRoot: "",
};

export function getSelectedService() {
  return state.managed.find((item) => item.service_id === state.selectedID) || null;
}
