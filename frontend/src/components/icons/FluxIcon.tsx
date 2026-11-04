/** Flux CD: two interlocking reconcile arrows. Line glyph, currentColor. */
const FluxIcon = ({ className = '', width = 15, height = 15 }: { className?: string; width?: number; height?: number }) => (
  <svg width={width} height={height} viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className}>
    <path d="M4.5 12a7.5 7.5 0 0 1 12.8-5.3" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    <path d="M17.6 3.6v3.4h-3.4" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    <path d="M19.5 12a7.5 7.5 0 0 1-12.8 5.3" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    <path d="M6.4 20.4V17h3.4" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    <circle cx="12" cy="12" r="1.8" fill="currentColor" />
  </svg>
);

export default FluxIcon;
