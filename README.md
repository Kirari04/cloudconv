Go Video Converter
==================

This is a simple web-based video converter built with Go. It uses the `chi` router for handling HTTP requests and `ffmpeg` for the actual video conversion. The frontend is a single HTML page styled with Tailwind CSS.

⚠️ Prerequisites
----------------

Before you can run this application, you **MUST** have the following software installed on your system and available in your system's PATH:

1.  [**Go**](https://go.dev/doc/install "null") (version 1.18 or newer)

2.  [**FFmpeg**](https://ffmpeg.org/download.html "null") - This is the core engine that performs all video conversions. The Go application simply calls this command-line tool.

You can verify they are installed correctly by running `go version` and `ffmpeg -version` in your terminal.

⚙️ Setup & Installation
-----------------------

1.  **Clone the Repository or Save the Files:** Download the files (`main.go`, `go.mod`, and the `templates/index.html` file) into a new project directory. Make sure to place `index.html` inside a `templates` subdirectory.

    Your project structure should look like this:

    ```
    /video-converter
    |-- go.mod
    |-- main.go
    |-- /templates
        |-- index.html

    ```

2.  **Initialize Go Module & Tidy Dependencies:** Open your terminal in the root of the project directory (`/video-converter`) and run the following commands:

    ```
    # This will download the chi router dependency specified in go.mod
    go mod tidy

    ```

▶️ How to Run the Application
-----------------------------

With Go and FFmpeg installed and the project set up, run the following command from the root of your project directory:

```
go run main.go

```

You will see a message in your terminal indicating that the server has started:

```
Starting server on http://localhost:3000

```

Now, open your web browser and navigate to **http://localhost:3000**.

🚀 How to Use
-------------

1.  Click the "Choose File" button to select a video from your computer.

2.  Select the desired output format from the dropdown menu (e.g., MP4, WebM, GIF).

3.  Click the "Convert Video" button.

4.  The page will reload. If the conversion is successful, a green success message will appear with a link to download your converted file. If it fails, a red error message will be shown.

Converted files are saved in the `converted` directory, and original uploads are stored in the `uploads` directory within your project folder.