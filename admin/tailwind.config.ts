import type { Config } from "tailwindcss";

const config: Config = {
  darkMode: "class",
  content: [
    "./app/**/*.{js,ts,jsx,tsx,mdx}",
    "./components/**/*.{js,ts,jsx,tsx,mdx}",
  ],
  theme: {
    extend: {
      colors: {
        sun: {
          50: "#fffcea",
          100: "#fff7c5",
          200: "#ffed85",
          300: "#ffde46",
          400: "#ffcc1b",
          500: "#ffae00",
          600: "#e28500",
          700: "#bb5c02",
          800: "#984608",
          900: "#7c3a0b",
          950: "#481d00",
        },
        brand: {
          50: "#fffcea",
          100: "#fff7c5",
          200: "#ffed85",
          300: "#ffde46",
          400: "#ffcc1b",
          DEFAULT: "#ffae00",
          600: "#e28500",
          700: "#bb5c02",
          800: "#984608",
          900: "#7c3a0b",
          950: "#481d00",
          dark: "#e28500",
        },
        surface: {
          light: "#ffffff",
          dark: "#0d121c",
          "dark-subtle": "#111827",
        },
      },
      boxShadow: {
        "elevation-sm": "0 1px 2px 0 rgba(0, 0, 0, 0.05)",
        "elevation-md": "0 4px 6px -1px rgba(0, 0, 0, 0.07), 0 2px 4px -2px rgba(0, 0, 0, 0.05)",
        "elevation-lg": "0 10px 15px -3px rgba(0, 0, 0, 0.08), 0 4px 6px -4px rgba(0, 0, 0, 0.04)",
        "elevation-xl": "0 20px 25px -5px rgba(0, 0, 0, 0.1), 0 8px 10px -6px rgba(0, 0, 0, 0.05)",
        "glow-brand": "0 0 20px -3px rgba(255, 174, 0, 0.45)",
        "card-dark": "0 4px 20px -2px rgba(0, 0, 0, 0.5), 0 0 0 1px rgba(255, 255, 255, 0.06)",
        "card-light": "0 4px 20px -2px rgba(0, 0, 0, 0.05), 0 0 0 1px rgba(0, 0, 0, 0.06)",
      },
    },
  },
  plugins: [],
};

export default config;
