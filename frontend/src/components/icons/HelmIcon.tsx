const HelmIcon = ({ className = '', width = 15, height = 15 }: { className?: string; width?: number; height?: number }) => (
  <svg
    width={width}
    height={height}
    viewBox="0 0 24 24"
    fill="none"
    xmlns="http://www.w3.org/2000/svg"
    className={className}
  >
    <path
      d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm0 18c-4.41 0-8-3.59-8-8s3.59-8 8-8 8 3.59 8 8-3.59 8-8 8z"
      fill="currentColor"
      opacity="0.3"
    />
    <circle cx="12" cy="12" r="2.5" fill="currentColor" />
    <rect x="11" y="4" width="2" height="5" fill="currentColor" />
    <rect x="11" y="15" width="2" height="5" fill="currentColor" />
    <rect x="4" y="11" width="5" height="2" fill="currentColor" />
    <rect x="15" y="11" width="5" height="2" fill="currentColor" />
    <rect
      x="7.17" y="7.17" width="2" height="4"
      transform="rotate(-45 8.17 9.17)"
      fill="currentColor"
    />
    <rect
      x="14.83" y="14.83" width="2" height="4"
      transform="rotate(-45 15.83 16.83)"
      fill="currentColor"
    />
    <rect
      x="7.17" y="14.83" width="2" height="4"
      transform="rotate(45 8.17 16.83)"
      fill="currentColor"
    />
    <rect
      x="14.83" y="7.17" width="2" height="4"
      transform="rotate(45 15.83 9.17)"
      fill="currentColor"
    />
  </svg>
);

export default HelmIcon;

