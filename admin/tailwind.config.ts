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
        kelp: {
          50: "#f4f6ef",
          100: "#e6eadd",
          200: "#cfd7bf",
          300: "#b0be98",
          400: "#94a576",
          500: "#778a58",
          600: "#5c6c44",
          700: "#485437",
          800: "#3a432e",
          900: "#343c2b",
          950: "#1a1f14",
        },
        brand: {
          50: "#f4f6ef",
          100: "#e6eadd",
          200: "#cfd7bf",
          300: "#b0be98",
          400: "#94a576",
          DEFAULT: "#778a58",
          600: "#5c6c44",
          700: "#485437",
          800: "#3a432e",
          900: "#343c2b",
          950: "#1a1f14",
          dark: "#5c6c44",
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
        "glow-brand": "0 0 20px -3px rgba(119, 138, 88, 0.4)",
        "card-dark": "0 4px 20px -2px rgba(0, 0, 0, 0.5), 0 0 0 1px rgba(255, 255, 255, 0.06)",
        "card-light": "0 4px 20px -2px rgba(0, 0, 0, 0.05), 0 0 0 1px rgba(0, 0, 0, 0.06)",
      },
    },
  },
  plugins: [],
};

export default config;
