// shared control icons, drawn from Google's Material Symbols (Apache-2.0,
// grade-500 weight). words travel in the consuming control's tooltip and
// aria label, never in the icon itself.

function MaterialIcon({ d, size }: { d: string; size?: number }) {
  return (
    <svg
      viewBox="0 -960 960 960"
      width={size}
      height={size}
      fill="currentColor"
      aria-hidden="true"
    >
      <path d={d} />
    </svg>
  );
}

// material symbols "add_box"; the new-conversation mark, the same glyph flo's
// toolbar uses for "new work"
export function AddBoxIcon() {
  return (
    <MaterialIcon d="M440-280h80v-160h160v-80H520v-160h-80v160H280v80h160v160ZM200-120q-33 0-56.5-23.5T120-200v-560q0-33 23.5-56.5T200-840h560q33 0 56.5 23.5T840-760v560q0 33-23.5 56.5T760-120H200Zm0-80h560v-560H200v560Zm0-560v560-560Z" />
  );
}

// material symbols "forum"; the conversation rail. grade-500 path from the
// official material-design-icons set
export function ForumIcon() {
  return (
    <MaterialIcon d="M281.2-227.56q-19.63 0-32.57-13.09-12.93-13.08-12.93-32.42v-85.5h526.93v-366.45h85.5q19.34 0 32.42 13.08 13.08 13.08 13.08 32.42v614.11L731.48-227.56H281.2ZM66.37-274.98v-574.11q0-19.33 13.08-32.42 13.08-13.08 32.42-13.08h525.26q19.34 0 32.42 13.08 13.08 13.09 13.08 32.42v365.02q0 19.34-13.08 32.42-13.08 13.08-32.42 13.08H230.2L66.37-274.98Zm525.26-254.59v-274.02H157.37v274.02h434.26Zm-434.26 0v-274.02 274.02Z" />
  );
}

// material symbols "upload"; the export action. grade-500 path from the
// official material-design-icons set
export function UploadIcon() {
  return (
    <MaterialIcon d="M434.5-322.87v-310.69L332.41-531.24l-63.89-65.41L480-808.13l211.48 211.48-63.89 65.41L525.5-633.56v310.69h-91Zm-191.63 171q-37.78 0-64.39-26.61t-26.61-64.39v-120h91v120h474.26v-120h91v120q0 37.78-26.61 64.39t-64.39 26.61H242.87Z" />
  );
}

// material symbols "psychology"; the model control's mark. grade-500 path
// from the official material-design-icons set
export function PsychologyIcon() {
  return (
    <MaterialIcon d="M232.83-72.83v-176.06q-56.76-52.96-88.38-123.41-31.62-70.46-31.62-147.7 0-152.99 107.07-260.08 107.07-107.09 260.03-107.09 127.46 0 225.87 75.05 98.42 75.05 128.13 195.45l52.48 207.15q5.72 21.63-7.91 39.16-13.63 17.53-36.35 17.53h-74.98v109q0 37.79-26.6 64.4-26.61 26.6-64.4 26.6h-69v80h-91v-171h160v-200h107.05l-36.81-150.21q-23-89.33-97.04-145.73-74.04-56.4-169.37-56.4-114.41 0-195.29 79.88Q203.83-636.4 203.83-522q0 59.04 24.14 112.57 24.14 53.52 68.66 94.56l27.2 25.2v216.84h-91ZM493.52-434.5ZM440-360h80l6-50q8-3 14.5-7t11.45-9l45.57 20L638-474l-40-30q2-8 2-16t-2-16l40-30-40.48-68-45.57 20q-4.95-5-11.45-9-6.5-4-14.5-7l-6-50h-80l-6 50q-8 3-14.5 7t-11.45 9l-45.57-20L322-566l40 30q-2 8-2 16t2 16l-40 30 40.48 68 45.57-20q4.95 5 11.45 9 6.5 4 14.5 7l6 50Zm40-100q-25 0-42.5-17.5T420-520q0-25 17.5-42.5T480-580q25 0 42.5 17.5T540-520q0 25-17.5 42.5T480-460Z" />
  );
}

// material symbols "description"; the system prompt's door. the same glyph
// flo's icons set carries
export function DescriptionIcon() {
  return (
    <MaterialIcon d="M320-239.28h320v-83.59H320v83.59Zm0-160h320v-83.59H320v83.59ZM242.87-71.87q-37.78 0-64.39-26.61t-26.61-64.39v-634.26q0-37.78 26.61-64.39t64.39-26.61h320.48l244.78 244.78v480.48q0 37.78-26.61 64.39t-64.39 26.61H242.87Zm274.26-525.26v-200H242.87v634.26h474.26v-434.26h-200Zm-274.26-200v200-200 634.26-634.26Z" />
  );
}

// material symbols "construction"; the tool panel's mark, wearing its count
// as a badge. grade-500 path from the official material-design-icons set
export function ConstructionIcon() {
  return (
    <MaterialIcon d="M759.11-111.87 537-333.98l89.02-89.26 222.11 222.11-89.02 89.26Zm-558.22 0-89.26-89.26 279.11-279.11-68.72-68.72-28 28-47.65-47.41v78.65l-29.67 29.68L88.76-587.98l29.67-29.67h78.66l-46.66-46.89 144.64-144.39q20.47-20.48 44.55-29.84t49.03-9.36q24.96 0 49.15 9.48 24.2 9.48 44.68 29.72l-93.44 93.19 49.76 49.76-28 28 68.96 68.96 89.76-89.76q-3.76-10.76-6.14-22.64T561-705.07q0-60.43 41.58-102.13 41.57-41.69 102.01-41.69 16.19 0 30.77 3.36 14.57 3.36 29.53 10.55l-99 99.24 69.37 69.37 99.24-99.24q8.2 14.72 11.29 29.53 3.1 14.82 3.1 31.01 0 60.44-41.93 102.02-41.94 41.57-102.37 41.57-11.76 0-23.41-2-11.64-2-22.4-6.52L200.89-111.87Z" />
  );
}