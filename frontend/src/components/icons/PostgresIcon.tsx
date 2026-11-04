/** PostgreSQL cluster: a database cylinder with a replica tick. Line glyph, currentColor. */
const PostgresIcon = ({ className = '', width = 15, height = 15 }: { className?: string; width?: number; height?: number }) => (
  <svg width={width} height={height} viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className}>
    <ellipse cx="11" cy="5.5" rx="6.5" ry="2.5" stroke="currentColor" strokeWidth="1.6" />
    <path d="M4.5 5.5v12c0 1.4 2.9 2.5 6.5 2.5" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    <path d="M17.5 5.5V11" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    <path d="M4.5 11.5c0 1.4 2.9 2.5 6.5 2.5" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    <circle cx="17.5" cy="17" r="3.5" stroke="currentColor" strokeWidth="1.6" />
    <path d="M16 17l1.1 1.1 2-2.2" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
  </svg>
);

export default PostgresIcon;
