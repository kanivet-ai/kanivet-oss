const NatsIcon = ({
  className = '',
  width = 15,
  height = 15,
}: {
  className?: string;
  width?: number;
  height?: number;
}) => (
  <svg
    width={width}
    height={height}
    viewBox="0 0 24 24"
    fill="none"
    xmlns="http://www.w3.org/2000/svg"
    className={className}
  >
    <circle cx="6" cy="12" r="2.4" fill="currentColor" />
    <path
      d="M11 8.2a5.4 5.4 0 0 1 0 7.6M14.4 5.4a9.4 9.4 0 0 1 0 13.2"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      fill="none"
    />
    <path
      d="M17.6 3a12.6 12.6 0 0 1 0 18"
      stroke="currentColor"
      strokeWidth="1.4"
      strokeLinecap="round"
      fill="none"
      opacity="0.55"
    />
  </svg>
);

export default NatsIcon;
