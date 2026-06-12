/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./templates/**/*.html"],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        // Decred brand palette.
        dcr: {
          green: "#2ED6A1",
          "green-dark": "#1fae82",
          blue: "#2970FF",
          navy: "#091440",
          "navy-light": "#0e1c52",
          surface: "#13235e",
        },
      },
      fontFamily: {
        sans: [
          "Inter",
          "ui-sans-serif",
          "system-ui",
          "-apple-system",
          "Segoe UI",
          "Roboto",
          "Helvetica Neue",
          "Arial",
          "sans-serif",
        ],
        mono: ["ui-monospace", "SFMono-Regular", "Menlo", "monospace"],
      },
    },
  },
  plugins: [],
};
