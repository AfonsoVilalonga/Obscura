from selenium import webdriver
from selenium.webdriver.firefox.service import Service
from selenium.webdriver.firefox.options import Options
from selenium.webdriver.common.by import By
import time

# Set up Firefox options
firefox_options = Options()

firefox_options.add_argument("--headless")  # Run Firefox in headless mode

firefox_options.set_preference("network.stricttransportsecurity.preloadlist", False)
firefox_options.set_preference("security.enterprise_roots.enabled", True)
firefox_options.set_preference("media.autoplay.default", 0)  # 0: Allow autoplay for all media
firefox_options.set_preference("media.autoplay.allow-muted", True)  # Allow muted autoplay
firefox_options.set_preference("media.autoplay.block-webaudio", False)  # Allow web audio autoplay
firefox_options.set_preference("media.peerconnection.ice.loopback", True)

# Path to the GeckoDriver (Firefox WebDriver)
geckodriver_path = "/media/sf_chromedriver-linux64/geckodriver"  # Replace with your geckodriver path

# Set up the WebDriver
service = Service(geckodriver_path)
driver = webdriver.Firefox(service=service, options=firefox_options)

try:
    # Open the specified URL
    url = "http://localhost:3000"
    driver.get(url)
    print(f"Opened {url} successfully.")

    # Block execution by keeping the browser running
    print("Firefox is running in headless mode. Press Ctrl+C to stop.")

    while True:
        time.sleep(5)  # Keep the script alive to maintain the browser session
        # input_element = driver.find_element(By.ID, 'inputField')
        # entered_value = input_element.get_attribute('value')
        # print(entered_value)
        # a = driver.find_element(By.ID, 'bits')
        # a = a.get_attribute('value')
        # print(a)

finally:
    # Clean up and close the driver
    driver.quit()
    print("Firefox browser closed.")
