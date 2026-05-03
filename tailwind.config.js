export default {
  content: ['./web/index.html', './web/src/**/*.ts'],
  theme: {
    extend: {
      colors: {
        brand: {
          50: '#f0f4ff',
          100: '#e1e9fe',
          200: '#c7d7fe',
          300: '#a3bcfe',
          400: '#7a96fd',
          500: '#536dfa',
          600: '#394df1',
          700: '#2b39db',
          800: '#2630b1',
          900: '#242d8c',
          950: '#161a52',
        },
      },
      fontFamily: {
        sans: ['"Inter"', 'ui-sans-serif', 'system-ui', 'sans-serif']
      },
      boxShadow: {
        'soft': '0 2px 15px -3px rgba(0, 0, 0, 0.07), 0 10px 20px -2px rgba(0, 0, 0, 0.04)',
      }
    }
  },
  plugins: []
};
