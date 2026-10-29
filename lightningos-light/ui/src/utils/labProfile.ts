// Presentation only: the separate regtest deployer inserts this static marker.
// Never use it to authorize requests or change financial/network policy.
export const isRegtestLab = () =>
  typeof document !== 'undefined' && document.getElementById('los-regtest-notice') !== null
