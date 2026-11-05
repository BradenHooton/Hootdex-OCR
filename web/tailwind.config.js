/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        bench: "#1B2130", // sorting-bench base
        panel: "#242C3D",
        raised: "#2D3649",
        rule: "#3A4459",
        ink: "#E7EAF0",
        muted: "#98A2B6",
        toploader: "#6FA8FF", // primary actions
        amber: "#F2B33D", // low-confidence fields
        good: "#4CC38A",
        bad: "#F0616D",
      },
      fontFamily: {
        sans: ['"Barlow"', "system-ui", "sans-serif"],
        display: ['"Barlow Semi Condensed"', '"Barlow"', "system-ui", "sans-serif"],
      },
      fontSize: {
        // modular scale ~1.2 for a dense workstation
        xs: ["0.75rem", { lineHeight: "1rem" }],
        sm: ["0.875rem", { lineHeight: "1.25rem" }],
        base: ["1rem", { lineHeight: "1.5rem" }],
        lg: ["1.2rem", { lineHeight: "1.6rem" }],
        xl: ["1.44rem", { lineHeight: "1.8rem" }],
        "2xl": ["1.728rem", { lineHeight: "2rem" }],
      },
    },
  },
  plugins: [],
};
